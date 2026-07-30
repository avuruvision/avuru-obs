# Releasing avuru-obs

How we version, branch, and cut releases. The model is borrowed from
[Kiali](https://github.com/kiali/kiali/blob/master/RELEASING.md) — a single
development trunk plus per-minor release branches — scaled to this project. For
the post-release verification steps, see
[RELEASE-CHECKLIST.md](RELEASE-CHECKLIST.md).

## What gets released

| Artifact | Where |
|---|---|
| `hub` container image | `ghcr.io/<org>/avuru-obs-hub` — `linux/amd64` + `linux/arm64`, cosign-signed, SBOM + provenance attached |
| `ui` container image | `ghcr.io/<org>/avuru-obs-ui` — same |
| `gateway` container image | `ghcr.io/<org>/avuru-obs-gateway` — same |
| `tdp-estimator` container image | `ghcr.io/<org>/avuru-obs-tdp-estimator` — same |
| Helm chart (`deploy/helm/avuruops`) | `oci://ghcr.io/<org>/charts/avuruops` (cosign-signed), **and** the `.tgz` attached to the GitHub Release |
| GitHub Release | tag `vX.Y.Z` + notes from [CHANGELOG.md](CHANGELOG.md) |

> The registry is parameterized in [`release.yml`](.github/workflows/release.yml)
> and defaults to GHCR, which the workflow authenticates to with the built-in
> `GITHUB_TOKEN` — nothing to configure. Point `REGISTRY`/`IMAGE_PREFIX`
> elsewhere only if you publish somewhere other than GHCR (you must then supply
> credentials).

The chart is the install path, so it ships to the registry alongside the images:

```bash
helm install avuruops oci://ghcr.io/<org>/charts/avuruops --version X.Y.Z \
  -n avuruops --create-namespace
```

The `.tgz` on the Release is the offline fallback for clients that cannot reach
a registry. (A classic `helm repo add` index is deliberately not published: OCI
reuses the same registry and credentials, and signs the same way.)

### Verifying a release

Signing is keyless — the identity is the workflow itself, so the certificate is
tied to `release.yml` at the release tag. Rename that workflow and these
commands change with it.

```bash
# images (the digest covers both architectures)
cosign verify ghcr.io/<org>/avuru-obs-hub:vX.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/<org>/avuru-obs/\.github/workflows/release\.yml@refs/tags/v'

# chart (SemVer tag, no leading v)
cosign verify ghcr.io/<org>/charts/avuruops:X.Y.Z \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/<org>/avuru-obs/\.github/workflows/release\.yml@refs/tags/v'

# both arches actually present
docker buildx imagetools inspect ghcr.io/<org>/avuru-obs-hub:vX.Y.Z
```

We sign the three images we build plus the chart. The runtime images the chart
pulls (collector, ClickHouse, OBI, profiler) are upstream and pinned in
`values.yaml` — their provenance is theirs, not ours.

## Versioning

- **Scheme:** [Semantic Versioning](https://semver.org) — `vX.Y.Z`.
- **In-development version** carries a `-SNAPSHOT` suffix and lives in the root
  [`VERSION`](VERSION) file — the **single source of truth**. `make version`
  prints it; `make version-set V=<x.y.z>` stamps it into `ui/package.json`, the
  chart (`Chart.yaml` `version`/`appVersion`, plus the image refs its Artifact
  Hub annotation lists) and the Hub build. Never hand-edit those.
- **Pre-1.0 caveat:** until `v1.0.0`, a minor bump (`0.Y`) may include breaking
  changes; patch bumps (`0.Y.Z`) are fixes only.

## Branch & tag model

```
main ───●───●───●───●───●───●──►   trunk: every PR lands here; carries X.Y.Z-SNAPSHOT
             \                 \
   v0.1 ──────●───●             ●──── v0.2 ...
            tag    tag         tag
          v0.1.0  v0.1.1      v0.2.0
                 (backport)
```

- **`main` is the trunk.** All contributions land on `main` via PR (see
  [CONTRIBUTING.md](CONTRIBUTING.md)). It always carries the next
  `-SNAPSHOT` version. There is no `develop` branch.
- **`vX.Y` release branches** are cut at the first release of a minor version
  and are the home for that minor's patch releases. Create one only when you cut
  a new minor (`vX.Y.0`).
- **`vX.Y.Z` tags** (signed) mark released commits.
- **Backports:** fixes land on `main` first, then are cherry-picked to the
  active `vX.Y` branch(es) for a patch release. Never develop directly on a
  release branch what hasn't been on `main`.

### Supported versions

Pre-1.0, we support the latest `main` and the most recent `vX.Y` release branch.
Security fixes land on `main` first (see [SECURITY.md](SECURITY.md)).

## Cadence

Releases are cut **as-ready** rather than on a fixed clock. (Kiali runs a 3-week
cron; we can add one later by extending `release.yml` with a `schedule:`
trigger — left out for now to avoid releasing on a timer before the project is
stable.)

## Cutting a release

Prerequisites: you are a maintainer with push rights, your commits are
[signed](COMMIT-SIGNING-SETUP.md), and `main` is green.

For a **new minor** `vX.Y.0`:

1. **Verify trunk is green:** `make check` passes on `main`.
2. **Finalize the changelog:** in [CHANGELOG.md](CHANGELOG.md), rename
   `## [Unreleased]` to `## [X.Y.0] — <date>` and add a fresh empty
   `Unreleased` block above it.
3. **Stamp the release version:** `make version-set V=X.Y.0` (drops `-SNAPSHOT`),
   then commit: `chore(release): vX.Y.0`.
4. **Tag and push** (signed): `git tag -s vX.Y.0 -m "vX.Y.0" && git push origin main --tags`.
5. **Create the release branch:** `git branch vX.Y vX.Y.0 && git push origin vX.Y`.
6. **Automation runs:** pushing the tag triggers
   [`release.yml`](.github/workflows/release.yml), which builds/pushes the `hub`
   and `ui` images, packages the Helm chart, and
   creates the GitHub Release with notes from the changelog. (If automation is
   unavailable, run the equivalent build steps locally — the workflow is the
   reference.)
7. **Bump trunk to the next snapshot:** on `main`, `make version-set
   V=X.(Y+1).0` then re-add `-SNAPSHOT` (`make version-set V=X.(Y+1).0-SNAPSHOT`),
   commit `chore: begin vX.(Y+1).0-SNAPSHOT`, open a PR.
8. **Verify** against [RELEASE-CHECKLIST.md](RELEASE-CHECKLIST.md).

For a **patch** `vX.Y.Z` (Z > 0):

1. Ensure the fix is merged on `main`, then cherry-pick it onto the `vX.Y` branch.
2. Update the changelog on `vX.Y` and `make version-set V=X.Y.Z`; commit.
3. Tag `vX.Y.Z` (signed) on the `vX.Y` branch and push; automation builds the
   patch the same way.

## Failure recovery

If the workflow fails partway, see the recovery notes at the end of
[RELEASE-CHECKLIST.md](RELEASE-CHECKLIST.md) — identify how far it got, clean up
partial artifacts (tag, branch, draft release, pushed images), and re-run.
