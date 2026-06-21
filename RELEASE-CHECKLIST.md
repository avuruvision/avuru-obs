# Release Checklist

Verify a release produced everything it should. Replace `X.Y.Z` (and `X.Y`) with
the actual version throughout. See [RELEASING.md](RELEASING.md) for the full
process and the branch/version model.

## Pre-release

- [ ] On `main` (for a minor) or the `vX.Y` branch (for a patch), `make check` is green.
- [ ] [CHANGELOG.md](CHANGELOG.md): `Unreleased` finalized into `## [X.Y.Z] — <date>`, with a fresh empty `Unreleased` above it.
- [ ] `make version-set V=X.Y.Z` applied (no `-SNAPSHOT`); `make version` prints `X.Y.Z`.
- [ ] Component versions agree: `agent/Cargo.toml` and `ui/package.json` read `X.Y.Z`.
- [ ] Release commit is **signed** (`git log --show-signature -1`).

## Cut

- [ ] Tag `vX.Y.Z` created and signed, pushed to the canonical remote.
      ```bash
      git tag -v vX.Y.Z        # verifies the signature
      ```
- [ ] For a new minor: `vX.Y` release branch created and pushed.
      ```bash
      git ls-remote --heads origin vX.Y
      ```

## Post-release verification

- [ ] **GitHub Release** exists for `vX.Y.Z` with notes and attached artifacts
      (agent binary, packaged Helm chart).
      ```bash
      gh release view vX.Y.Z
      ```
- [ ] **Hub image** published with `X.Y.Z` (and moving `X.Y`) tags.
      ```bash
      docker buildx imagetools inspect ghcr.io/<org>/avuru-obs-hub:vX.Y.Z
      ```
- [ ] **UI image** published with `X.Y.Z` (and moving `X.Y`) tags.
      ```bash
      docker buildx imagetools inspect ghcr.io/<org>/avuru-obs-ui:vX.Y.Z
      ```
- [ ] **Helm chart** `version`/`appVersion` in the packaged chart match `X.Y.Z`.
- [ ] `release.yml` run is green (Actions tab); no skipped publish steps.

## Post-release housekeeping

- [ ] On `main`, version bumped to the next `-SNAPSHOT`
      (`make version-set V=X.(Y+1).0-SNAPSHOT`) and committed/merged.
- [ ] [ROADMAP.md](ROADMAP.md) updated if the release closed or moved a milestone.
- [ ] Release announced (discussions / channels) if applicable.

## Failure recovery

If `release.yml` fails partway:

1. Open the failed run; read the **logs** to see which step failed and how far
   it got (tag pushed? branch created? images pushed? release drafted?).
2. Clean up partial artifacts so a re-run starts clean:
   - Delete the GitHub Release/draft: `gh release delete vX.Y.Z --yes`.
   - Delete the tag locally and remotely if it must be re-cut:
     `git tag -d vX.Y.Z && git push origin :refs/tags/vX.Y.Z`.
   - Delete a half-created `vX.Y` branch if it shouldn't exist yet.
   - Untag/overwrite any partially-pushed images (registry permitting).
3. Fix the cause, then **re-run the workflow** (re-push the tag, or use the
   `workflow_dispatch` fallback in `release.yml`).
4. Re-run this checklist from **Post-release verification**.
