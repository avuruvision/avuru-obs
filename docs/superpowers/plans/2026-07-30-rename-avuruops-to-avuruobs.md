# Rename `avuruops` → `avuruobs` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename every `avuruops` / `AVURUOPS` identifier (Helm chart name, k8s resource/label prefix, mount paths, GHCR OCI chart path, env-var prefix, and the `avuruops_quality` ClickHouse attribute) to `avuruobs` / `AVURUOBS`, so the deploy layer and env-var contract match the project's actual name (`avuru-obs`) before v0.3 is promoted publicly.

**Architecture:** This is a pure, deterministic string rename (`avuruops`→`avuruobs`, `AVURUOPS`→`AVURUOBS`), never a refactor of logic. Every task below is: (1) an exact list of files, (2) the exact `sed` command applied to them, (3) an exact command that proves the result still works. Tasks are ordered so each one leaves the repo in a state where its own verification command passes, even though later tasks haven't run yet (cross-file consistency is only required at the end — see Task 10).

**Tech Stack:** bash/sed (Darwin/BSD sed — `sed -i ''`), Helm 3, Go (5 modules: `hub`, `gateway/{avuruingestauth,tenantfromauth,sentryreceiver}`, `sensor/tdp-estimator`, `e2e`, `tools/seed`), Next.js/TypeScript (`ui/`), GitHub Actions.

**Base state:** `feature/green-tdp-estimation` (PR #70) merged into `main` at `043b919`. This plan runs in a fresh worktree branched directly from `origin/main` — not from the feature branch — so it never touches that branch's separate, still-uncommitted TDP-estimation WIP (`ci.yml`, `Makefile`, its own plan doc, `sensor/tdp-estimator/model_test.go` in the *original* checkout, which live elsewhere and are untouched by this worktree).

**Out of scope (do not touch):**
- `design/*.md`, `docs/superpowers/plans/*.md`, `docs/superpowers/specs/*.md`, `CHANGELOG.md` — these are dated records of what was true when written (e.g. CHANGELOG.md:275 documents the `AVURUOPS_PROJECTS` env var as it existed in a past release; `docs/superpowers/plans/2026-07-30-green-tdp-estimation.md` is now merged into `main` as part of that history too). Rewriting them falsifies history.
- `.claude/settings.local.json` — untracked by git (confirmed via `git ls-files`), not part of the codebase.
- `.claude/worktrees/**` — other in-progress sessions' isolated worktrees (`auth-ingest-keys`, `launch-readiness`, `projects-phase1-crud`, `ui-brand`, `ux-overhaul`). Never touch another worktree's checkout.
- `deploy/helm/avuruops/values.yaml`'s `image.repository: avuruops/hub|ui|gateway` defaults will mechanically become `avuruobs/hub|ui|gateway` in Task 2 below (pure substring rename) — these still won't match the *actually published* image names (`avuru-obs-hub`, etc.), same as before this rename. That mismatch is pre-existing (already worked around via explicit `--set` in `e2e-helm.sh` and `values-*.yaml`) and is not something this plan fixes.

**The rename command, reused throughout this plan:**
```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
```
Define this function once per shell session before using it in any task below.

---

### Task 1: Isolated worktree — DONE

Already completed for this plan run: a worktree was created at `.claude/worktrees/rename-avuruops-avuruobs` via the native `EnterWorktree` tool, which branches from `origin/main` by default. The branch was then renamed from its auto-generated name to `feature/rename-avuruops-avuruobs` to match this repo's branch-naming convention (`feature/<topic>`, never `ai/*` or the default `worktree-*` prefix).

The file scope below was re-verified by grepping `avuruops`/`AVURUOPS` fresh inside this worktree (not reused from an earlier, staler scoping pass) — three files turned up that weren't visible in the original scoping pass, folded into Tasks 6–7 below: `hub/internal/api/green.go`, `e2e/green_test.go`, `deploy/compose/seed/fixtures/metrics_kepler.json`.

- [x] **Step 1: Worktree created, branched from origin/main**

Verified:
```bash
git rev-parse HEAD    # 043b9194100063bb115d63acd255dbbcb34a25a4
git rev-parse origin/main   # same — clean checkout, no drift
git status --porcelain      # empty
```

- [x] **Step 2: Chart layout confirmed**

```bash
ls deploy/helm/avuruops/templates | wc -l   # 26
find deploy/helm/avuruops -type f | wc -l   # 30
```

All remaining tasks run inside this worktree (`.claude/worktrees/rename-avuruops-avuruobs`, branch `feature/rename-avuruops-avuruobs`).

---

### Task 2: Rename the Helm chart directory and its internal content

**Files:**
- Rename: `deploy/helm/avuruops/` → `deploy/helm/avuruobs/` (git mv, all 30 files under it: `Chart.yaml`, `.helmignore`, `values.schema.json`, `values.yaml`, `templates/*.yaml` ×24, `templates/_helpers.tpl`, `templates/NOTES.txt`)

- [ ] **Step 1: Move the directory with git mv (preserves history)**

```bash
git mv deploy/helm/avuruops deploy/helm/avuruobs
```

- [ ] **Step 2: Rename every avuruops/AVURUOPS occurrence inside it**

```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
find deploy/helm/avuruobs -type f -print0 | xargs -0 RENAME
```

- [ ] **Step 3: Verify no `avuruops`/`AVURUOPS` survives in the new chart dir**

```bash
grep -rli "avuruops" deploy/helm/avuruobs | wc -l
```
Expected: `0`

- [ ] **Step 4: Verify the chart still lints**

```bash
helm lint deploy/helm/avuruobs
```
Expected: `1 chart(s) linted, 0 chart(s) failed`

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/avuruobs
git commit -m "chore: rename Helm chart avuruops -> avuruobs"
```

---

### Task 3: Update the sibling Helm tooling (values overlays, template-test.sh, e2e-helm.sh)

These live outside `deploy/helm/avuruops/` (now `avuruobs/`) so Task 2's directory-scoped sed didn't touch them.

**Files:**
- Modify: `deploy/helm/values-external-clickhouse.yaml`
- Modify: `deploy/helm/values-lab.yaml`
- Modify: `deploy/helm/values-prod.yaml`
- Modify: `deploy/helm/values-staging.yaml`
- Modify: `deploy/helm/template-test.sh`
- Modify: `deploy/helm/e2e-helm.sh`

- [ ] **Step 1: Apply the rename**

```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
RENAME deploy/helm/values-external-clickhouse.yaml deploy/helm/values-lab.yaml \
       deploy/helm/values-prod.yaml deploy/helm/values-staging.yaml \
       deploy/helm/template-test.sh deploy/helm/e2e-helm.sh
```

- [ ] **Step 2: Verify no `avuruops`/`AVURUOPS` remains in `deploy/helm/` (chart dir already clean from Task 2)**

```bash
grep -rli "avuruops" deploy/helm | wc -l
```
Expected: `0`

- [ ] **Step 3: Run the render-time assertion suite (no cluster needed)**

```bash
make helm-check
```
Expected: last line `ALL TEMPLATE ASSERTIONS PASSED`

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/values-external-clickhouse.yaml deploy/helm/values-lab.yaml \
        deploy/helm/values-prod.yaml deploy/helm/values-staging.yaml \
        deploy/helm/template-test.sh deploy/helm/e2e-helm.sh
git commit -m "chore: rename avuruops -> avuruobs in Helm values overlays and test scripts"
```

---

### Task 4: Update CI/release automation

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `Makefile`
- Modify: `RELEASING.md`
- Modify: `.gitleaks.toml`

- [ ] **Step 1: Apply the rename**

```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
RENAME .github/workflows/ci.yml .github/workflows/release.yml Makefile RELEASING.md .gitleaks.toml
```

- [ ] **Step 2: Confirm release.yml's chart package/push/sign now agree with the new Chart.yaml name**

```bash
grep -n "avuruobs" .github/workflows/release.yml
```
Expected: 4 hits — `helm lint deploy/helm/avuruobs`, `helm package deploy/helm/avuruobs`, `helm push "dist/avuruobs-${VERSION}.tgz" ...`, `cosign sign ... "${CHART_NS}/avuruobs@${digest}"` (the `.tgz` filename must match what `helm package` actually emits, which is `<Chart.yaml name>-<version>.tgz` — `avuruobs-*.tgz` since Task 2 already renamed Chart.yaml's `name:` field).

- [ ] **Step 3: Verify YAML syntax didn't break**

```bash
python3 -c "import yaml,sys; [yaml.safe_load(open(f)) for f in ['.github/workflows/ci.yml','.github/workflows/release.yml']]" && echo OK
```
Expected: `OK`

- [ ] **Step 4: Re-run the fast Helm checks (Makefile path changed)**

```bash
make helm-check
```
Expected: `ALL TEMPLATE ASSERTIONS PASSED`

- [ ] **Step 5: Verify the version-set target still edits the right file**

```bash
grep -n "deploy/helm/avuruobs/Chart.yaml" Makefile
```
Expected: 3 hits (the `version-set` target's three `perl -i -pe` lines).

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/ci.yml .github/workflows/release.yml Makefile RELEASING.md .gitleaks.toml
git commit -m "chore: rename avuruops -> avuruobs in CI/release automation"
```

---

### Task 5: Update user-facing install docs and scripts

**Files:**
- Modify: `README.md`
- Modify: `deploy/helm/README.md`
- Modify: `deploy/helm/artifacthub-repo.yml`
- Modify: `deploy/install.sh`
- Modify: `deploy/demo/astronomy/install.sh`
- Modify: `deploy/demo/astronomy/README.md`
- Modify: `deploy/demo/astronomy/values-avuru.yaml`
- Modify: `deploy/demo/public/install.sh`
- Modify: `deploy/demo/public/values-public-demo.yaml`
- Modify: `docs/runbooks/app-probe-failures.md`
- Modify: `docs/runbooks/public-demo.md`
- Modify: `docs/runbooks/sensor-rollout.md`

- [ ] **Step 1: Apply the rename**

```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
RENAME README.md deploy/helm/README.md deploy/helm/artifacthub-repo.yml \
       deploy/install.sh \
       deploy/demo/astronomy/install.sh deploy/demo/astronomy/README.md deploy/demo/astronomy/values-avuru.yaml \
       deploy/demo/public/install.sh deploy/demo/public/values-public-demo.yaml \
       docs/runbooks/app-probe-failures.md docs/runbooks/public-demo.md docs/runbooks/sensor-rollout.md
```

- [ ] **Step 2: Shellcheck the two installers (they're curl-piped to `sh`, worth the extra check)**

```bash
shellcheck deploy/install.sh deploy/demo/astronomy/install.sh deploy/demo/public/install.sh 2>&1 | head -50
```
Expected: no new findings versus running the same command on the pre-rename versions (a pure string substitution cannot introduce a shellcheck issue that wasn't already there). If `shellcheck` isn't installed, skip this step — it's a bonus check, not a gate.

- [ ] **Step 3: Verify no `avuruops`/`AVURUOPS` remains in this file set**

```bash
grep -li "avuruops" README.md deploy/helm/README.md deploy/helm/artifacthub-repo.yml \
  deploy/install.sh deploy/demo/astronomy/install.sh deploy/demo/astronomy/README.md \
  deploy/demo/astronomy/values-avuru.yaml deploy/demo/public/install.sh \
  deploy/demo/public/values-public-demo.yaml docs/runbooks/app-probe-failures.md \
  docs/runbooks/public-demo.md docs/runbooks/sensor-rollout.md | wc -l
```
Expected: `0`

- [ ] **Step 4: Commit**

```bash
git add README.md deploy/helm/README.md deploy/helm/artifacthub-repo.yml deploy/install.sh \
        deploy/demo/astronomy/install.sh deploy/demo/astronomy/README.md deploy/demo/astronomy/values-avuru.yaml \
        deploy/demo/public/install.sh deploy/demo/public/values-public-demo.yaml \
        docs/runbooks/app-probe-failures.md docs/runbooks/public-demo.md docs/runbooks/sensor-rollout.md
git commit -m "docs: rename avuruops -> avuruobs in install docs and demo scripts"
```

---

### Task 6: Rename the AVURUOPS_ env-var prefix and the avuruops_quality attribute in Go source (hub + gateway)

This is the biggest-blast-radius piece: `AVURUOPS_*` is read via `os.Getenv`/`envOr` across the hub's `cmd/hub` and `internal/*` packages, plus one gateway module. It also covers the `avuruops_quality` ClickHouse attribute the green module reads/writes (`hub/internal/storage/store.go`, `hub/internal/storage/clickhouse/green.go` and its integration test, and `hub/internal/api/green.go`) — this data isn't in production yet (green module is pre-real-RAPL-validation), so renaming the attribute key now is safe.

**Files:**
- Modify: `hub/cmd/hub/main.go`
- Modify: `hub/cmd/hub/alerting.go`
- Modify: `hub/cmd/hub/green.go`
- Modify: `hub/cmd/hub/green_test.go`
- Modify: `hub/cmd/hub/groups.go`
- Modify: `hub/cmd/hub/ingestseed.go`
- Modify: `hub/cmd/hub/oidc.go`
- Modify: `hub/cmd/hub/oidc_test.go`
- Modify: `hub/internal/alerting/config.go`
- Modify: `hub/internal/green/config.go`
- Modify: `hub/internal/health/config.go`
- Modify: `hub/internal/api/projects.go`
- Modify: `hub/internal/api/router.go`
- Modify: `hub/internal/api/green.go`
- Modify: `hub/internal/modules/modules.go`
- Modify: `hub/internal/storage/store.go`
- Modify: `hub/internal/storage/clickhouse/green.go`
- Modify: `hub/internal/storage/clickhouse/green_integration_test.go`
- Modify: `gateway/avuruingestauth/config.go`

- [ ] **Step 1: Apply the rename**

```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
RENAME hub/cmd/hub/main.go hub/cmd/hub/alerting.go hub/cmd/hub/green.go hub/cmd/hub/green_test.go \
       hub/cmd/hub/groups.go hub/cmd/hub/ingestseed.go hub/cmd/hub/oidc.go hub/cmd/hub/oidc_test.go \
       hub/internal/alerting/config.go hub/internal/green/config.go hub/internal/health/config.go \
       hub/internal/api/projects.go hub/internal/api/router.go hub/internal/api/green.go \
       hub/internal/modules/modules.go \
       hub/internal/storage/store.go hub/internal/storage/clickhouse/green.go \
       hub/internal/storage/clickhouse/green_integration_test.go \
       gateway/avuruingestauth/config.go
```

- [ ] **Step 2: Verify no `avuruops`/`AVURUOPS` remains in hub or gateway source**

```bash
grep -rli "avuruops" hub gateway | wc -l
```
Expected: `0`

- [ ] **Step 3: Build and unit-test the hub**

```bash
cd hub && go build ./... && go test -race ./...
cd ..
```
Expected: build succeeds; all tests pass (the ClickHouse-backed `green_integration_test.go` needs a running ClickHouse/testcontainers — if it's skipped locally without one, that's expected and not a regression from this rename).

- [ ] **Step 4: Build, vet and test the gateway module**

```bash
cd gateway/avuruingestauth && go build ./... && go vet ./... && go test ./...
cd ../..
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add hub gateway/avuruingestauth
git commit -m "refactor: rename AVURUOPS_ env prefix and avuruops_quality attribute to avuruobs"
```

---

### Task 7: Update e2e Go tests, docker-compose files, seed fixtures, and diagnostic/screenshot tooling

**Files:**
- Modify: `e2e/auth_helpers_test.go`
- Modify: `e2e/dropin_test.go`
- Modify: `e2e/helm_test.go`
- Modify: `e2e/ingest_keys_test.go`
- Modify: `e2e/oidc_test.go`
- Modify: `e2e/tenant_test.go`
- Modify: `e2e/green_test.go`
- Modify: `deploy/compose/docker-compose.yaml`
- Modify: `deploy/compose/docker-compose.oidc-e2e.yaml`
- Modify: `deploy/compose/docker-compose.ingest-e2e.yaml`
- Modify: `deploy/compose/docker-compose.release.yaml`
- Modify: `deploy/compose/oidc-e2e.yaml`
- Modify: `deploy/compose/gateway/config.ingest-e2e.yaml`
- Modify: `deploy/compose/gateway/config.ingest-e2e-log.yaml`
- Modify: `deploy/compose/seed/fixtures/metrics_kepler.json`
- Modify: `tools/diagnose/sensor-impact.sh`
- Modify: `tools/screenshots/compose.screenshots.yaml`

- [ ] **Step 1: Apply the rename**

```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
RENAME e2e/auth_helpers_test.go e2e/dropin_test.go e2e/helm_test.go e2e/ingest_keys_test.go \
       e2e/oidc_test.go e2e/tenant_test.go e2e/green_test.go \
       deploy/compose/docker-compose.yaml deploy/compose/docker-compose.oidc-e2e.yaml \
       deploy/compose/docker-compose.ingest-e2e.yaml deploy/compose/docker-compose.release.yaml \
       deploy/compose/oidc-e2e.yaml deploy/compose/gateway/config.ingest-e2e.yaml \
       deploy/compose/gateway/config.ingest-e2e-log.yaml \
       deploy/compose/seed/fixtures/metrics_kepler.json \
       tools/diagnose/sensor-impact.sh tools/screenshots/compose.screenshots.yaml
```

- [ ] **Step 2: Verify no `avuruops`/`AVURUOPS` remains in this file set**

```bash
grep -rli "avuruops" e2e deploy/compose tools/diagnose tools/screenshots | wc -l
```
Expected: `0`

- [ ] **Step 3: Build the e2e module for both build tags used in this repo**

```bash
cd e2e && go build -tags=e2e ./... && go build -tags=e2ehelm ./... && go vet ./...
cd ..
```
Expected: all succeed (these are compile-only checks; the tests themselves need a live compose/kind stack, out of scope for this plan — see Task 10's manual follow-up note).

- [ ] **Step 4: Sanity-check the compose and fixture files parse**

```bash
python3 -c "
import yaml, json
for f in ['deploy/compose/docker-compose.yaml','deploy/compose/docker-compose.oidc-e2e.yaml',
          'deploy/compose/docker-compose.ingest-e2e.yaml','deploy/compose/docker-compose.release.yaml',
          'deploy/compose/oidc-e2e.yaml','deploy/compose/gateway/config.ingest-e2e.yaml',
          'deploy/compose/gateway/config.ingest-e2e-log.yaml','tools/screenshots/compose.screenshots.yaml']:
    yaml.safe_load(open(f))
json.load(open('deploy/compose/seed/fixtures/metrics_kepler.json'))
print('OK')
"
```
Expected: `OK`

- [ ] **Step 5: Commit**

```bash
git add e2e deploy/compose tools/diagnose/sensor-impact.sh tools/screenshots/compose.screenshots.yaml
git commit -m "chore: rename avuruops -> avuruobs in e2e tests, compose files, seed fixtures, and tooling"
```

---

### Task 8: Update UI Playwright config and specs

**Files:**
- Modify: `ui/e2e-screenshots/capture.spec.ts`
- Modify: `ui/e2e/auth.spec.ts`
- Modify: `ui/playwright.config.ts`
- Modify: `ui/playwright.screenshots.config.ts`

- [ ] **Step 1: Apply the rename**

```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
RENAME ui/e2e-screenshots/capture.spec.ts ui/e2e/auth.spec.ts \
       ui/playwright.config.ts ui/playwright.screenshots.config.ts
```

- [ ] **Step 2: Verify no `avuruops`/`AVURUOPS` remains under ui/**

```bash
grep -rli "avuruops" ui | wc -l
```
Expected: `0`

- [ ] **Step 3: Typecheck**

```bash
cd ui && npm run typecheck
cd ..
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add ui/e2e-screenshots/capture.spec.ts ui/e2e/auth.spec.ts ui/playwright.config.ts ui/playwright.screenshots.config.ts
git commit -m "chore: rename avuruops -> avuruobs in UI Playwright config/specs"
```

---

### Task 9: Update conventions docs (agent_docs, module READMEs)

These describe *current* conventions (not dated history), so they should track the rename.

**Files:**
- Modify: `agent_docs/tech_stack.md`
- Modify: `agent_docs/testing.md`
- Modify: `agent_docs/architecture.md`
- Modify: `agent_docs/go_style.md`
- Modify: `sensor/README.md`
- Modify: `gateway/schemas/README.md`
- Modify: `deploy/compose/README.md`
- Modify: `tools/screenshots/README.md`

- [ ] **Step 1: Apply the rename**

```bash
RENAME() { sed -i '' -e 's/avuruops/avuruobs/g' -e 's/AVURUOPS/AVURUOBS/g' "$@"; }
RENAME agent_docs/tech_stack.md agent_docs/testing.md agent_docs/architecture.md agent_docs/go_style.md \
       sensor/README.md gateway/schemas/README.md deploy/compose/README.md tools/screenshots/README.md
```

- [ ] **Step 2: Verify no `avuruops`/`AVURUOPS` remains in this file set**

```bash
grep -li "avuruops" agent_docs/tech_stack.md agent_docs/testing.md agent_docs/architecture.md \
  agent_docs/go_style.md sensor/README.md gateway/schemas/README.md deploy/compose/README.md \
  tools/screenshots/README.md | wc -l
```
Expected: `0`

- [ ] **Step 3: Commit**

```bash
git add agent_docs/tech_stack.md agent_docs/testing.md agent_docs/architecture.md agent_docs/go_style.md \
        sensor/README.md gateway/schemas/README.md deploy/compose/README.md tools/screenshots/README.md
git commit -m "docs: rename avuruops -> avuruobs in convention docs"
```

---

### Task 10: Full-repo verification sweep and final checks

**Files:** none (verification only)

- [ ] **Step 1: Repo-wide leftover scan, excluding the intentionally-untouched paths**

```bash
grep -rlI "avuruops\|AVURUOPS" --exclude-dir=.git --exclude-dir=.claude . \
  | grep -vE "^\./design/|^\./docs/superpowers/plans/|^\./docs/superpowers/specs/|^\./CHANGELOG.md$"
```
Expected: empty output (nothing printed). If something prints, it's a file this plan missed — add a step to rename it before proceeding.

- [ ] **Step 2: Full build/test/lint sweep**

```bash
make check
```
Expected: hub build+race-tests pass; every `GATEWAY_MODULES` (`sentryreceiver avuruingestauth tenantfromauth`) and `SENSOR_MODULES` (`tdp-estimator`) builds, vets and tests clean; `ui` lints and builds.

- [ ] **Step 3: Fast Helm checks one more time (belt and suspenders after all the doc/CI edits)**

```bash
make helm-check
```
Expected: `ALL TEMPLATE ASSERTIONS PASSED`

- [ ] **Step 4: golangci-lint on the hub (required by this repo's convention before pushing Go changes)**

```bash
cd hub && golangci-lint run
cd ..
```
Expected: no findings.

- [ ] **Step 5: Note remaining manual/live-infra verification for the user**

Not run as part of this plan (they need a live kind cluster / docker daemon and take several minutes):
```bash
make e2e-helm   # kind cluster + helm install avuruobs + wedge-gate assertions
make e2e-ui     # compose stack + Playwright against the renamed env vars
```
Flag to the user that these should be run at least once before merging, since they're the only checks that exercise the renamed chart against a real cluster / the renamed `AVURUOBS_*` env vars against a real compose stack end-to-end.

- [ ] **Step 6: Final commit (if any stray fixups were needed in Step 1)**

```bash
git add -A
git status --porcelain   # confirm nothing unexpected is staged
git commit -m "chore: fix up remaining avuruops references found in verification sweep"
```
(Skip this step entirely if Step 1 found nothing to fix.)

---

## Post-plan: what the user still needs to do

- Run `make e2e-helm` and `make e2e-ui` at least once (live infra, not automated by this plan).
- This worktree's branch (`feature/rename-avuruops-avuruobs`) was branched directly from `origin/main` — open a PR straight against `main` (no intermediate feature branch to reconcile, since `feature/green-tdp-estimation` is already merged).
- No GHCR/OCI compatibility concern: v0.2.0 published `oci://ghcr.io/avuruvision/charts/avuruops`, but the user is the only consumer so far and this rename happens before the v0.3 promotion.
