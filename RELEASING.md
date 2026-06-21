# Releasing avuru-obs

How we version, branch, and cut releases. The model is borrowed from
[Kiali](https://github.com/kiali/kiali/blob/master/RELEASING.md) — a single
development trunk plus per-minor release branches — scaled to this project. For
the post-release verification steps, see
[RELEASE-CHECKLIST.md](RELEASE-CHECKLIST.md).

## What gets released

| Artifact | Where |
|---|---|
| `hub` container image | container registry (`ghcr.io/<org>/avuru-obs-hub`, configurable) |
| `ui` container image | container registry (`ghcr.io/<org>/avuru-obs-ui`, configurable) |
| `agent` binary | attached to the GitHub Release |
| Helm chart (`deploy/helm/avuruops`) | packaged and attached to the GitHub Release |
| GitHub Release | tag `vX.Y.Z` + notes from [CHANGELOG.md](CHANGELOG.md) |

> The registry is parameterized in [`release.yml`](.github/workflows/release.yml)
> and defaults to GHCR. No registry is wired to real infrastructure yet — set
> the `REGISTRY`/`IMAGE_PREFIX` inputs (and credentials) when one is.

## Versioning

- **Scheme:** [Semantic Versioning](https://semver.org) — `vX.Y.Z`.
- **In-development version** carries a `-SNAPSHOT` suffix and lives in the root
  [`VERSION`](VERSION) file — the **single source of truth**. `make version`
  prints it; `make version-set V=<x.y.z>` stamps it into `agent/Cargo.toml`,
  `ui/package.json`, and the Hub build.
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
   and `ui` images, builds the `agent` binary, packages the Helm chart, and
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
