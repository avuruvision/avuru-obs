# AEP: Authentication, RBAC and per-project ingest keys

- **Date:** 2026-07-21
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

Build the auth layer the
[enterprise seam](../agent_docs/architecture.md#enterprise-seam-do-not-bypass)
promised: a `hub/internal/auth` package with a `Provider` interface and two
implementations — **local users** (email + bcrypt password, bootstrap admin)
and **OIDC** (authorization-code flow at the hub; Keycloak, Entra, Okta,
Google, Dex all work; LDAP/AD arrive by federating through the IdP, never as
hub code). Authorization is **fixed roles × per-project grants**: Admin,
Editor, Viewer, each scoped to named projects or `*` (global). The
`X-Avuru-Tenant` header stops being trusted and starts being **validated
against the caller's grants** — a user sees exactly the projects they are
granted, which turns projects from a UX filter into a boundary. An opt-in
**anonymous role scoped to explicitly listed projects** keeps a public demo
possible against a real instance. On the write side, **per-project ingest API
keys** (validated by a small custom gateway auth extension) replace topology
trust of `avuru.tenant`. Auth is **core with an off-switch** (`auth.enabled`,
default **true**), not a module — identity gates everything, so it cannot be a
signal-family toggle. No new cluster components: no Dex, no oauth2-proxy, no
new pods; state lives in ClickHouse like everything else.

## Motivation

Today the hub has **no auth at all** — every endpoint is open, including
mutating ones (alert-channel CRUD, error triage), and `tenant(r)` reads
`X-Avuru-Tenant` straight off the request, so any visitor can read (or write
into) **any** project by setting a header. Anyone who can reach the hub —
e.g. the docs-site live demo pointing at a real LAN instance — sees every
project on it. This AEP is the roadmap's v0.2 auth item, and it unblocks the
queued Projects work (API keys at ingest, CRUD, per-project retention).

Ties to the [wedge](../AGENTS.md): a login screen does not break "fresh
cluster → live service map in under 5 minutes" — the bootstrap admin password
is a Helm value/secret and the TTV gate logs in before asserting. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
the hub stays out of the telemetry byte-path — ingest keys are validated **in
the gateway** (cached), the hub only answers the gateway's control-plane
validation calls. The design follows the prior art consensus (Coroot,
Grafana, Sentry): server-side cookie sessions, three fixed roles, bootstrap
admin, per-project ingest keys — with two deliberate deviations in the OSS
user's favor: **OIDC ships in OSS** (Coroot gates SSO behind Enterprise), and
**anonymous access is project-scoped** (Coroot's is instance-wide).

### Goals

- **Secure by default**: `auth.enabled=true`; fresh installs bootstrap a
  global-Admin `admin` user from a Helm secret (auto-generated when unset);
  existing installs upgrade into a login screen. `auth.enabled=false` restores
  today's open behavior (labs, air-gapped demos).
- **Local users + OIDC** behind one `auth.Provider` seam; both end in the same
  server-side session (HttpOnly cookie, instant revocation). LDAP/SAML via IdP
  federation only.
- **RBAC**: Admin / Editor / Viewer × per-project grants (`*` = global).
  Viewer: all reads. Editor: triage writes on granted projects. Admin
  (global): users, grants, alert channels, ingest keys, settings.
- **Project isolation**: granted projects are the only ones a caller can
  select, list, or query. 403 on everything else.
- **OIDC → grant mapping** in config (hot-reloadable): IdP groups map to
  role+projects; unmatched SSO users get `defaultRole`/`defaultProjects`;
  SSO users are auto-created on first login; `forceSSO` hides local login.
- **Anonymous demo mode**: `auth.anonymous.{enabled, role, projects}` — a
  synthetic Viewer identity granted only the listed projects.
- **Per-project ingest keys**: UI-managed (hashed at rest, shown once) plus
  chart-declared static keys (secretRefs) — the config+UI hybrid alerting
  channels established. The key's project **overrides** client-supplied
  `avuru.tenant`. The Sentry DSN public key is an ingest key, so browser
  events get stamped the same way. The chart auto-generates the sensor's key.
- **Gateway enforcement** via a custom OCB server-auth extension with cached
  hub validation, rolled out through `auth.ingest.mode: off | log | enforce`,
  default **`log`** in v0.2 (observe would-be denials; never silently drop
  telemetry on upgrade — the OTLP drop-in promise holds until an install
  explicitly flips to `enforce`).

### Non-goals (v1)

- **Custom roles / fine-grained permissions** — the fixed three are the OSS
  contract (Coroot/Grafana parity); custom roles remain an enterprise-seam
  extension.
- **SAML, native LDAP binds** — federate through Keycloak/Dex; OIDC is the
  only protocol the hub speaks.
- **Personal API tokens / service accounts** for the hub API (the future
  Grafana-datasource/CLI clients) — additive later on the same session layer.
- **Audit log UI** — auth events go to structured logs for now.
- **Per-user preferences, teams, invitations by email.**
- **Multi-replica session affinity concerns** — sessions live in ClickHouse,
  so any hub replica can serve any session; nothing to do, noted for clarity.

## Solution

### Components and data flow

```
Browser ──login──▶ hub /api/v1/auth/*  ──▶ auth.Provider (local │ oidc)
   │                                        │
   │◀─ HttpOnly session cookie ─────────────┘  session row in ClickHouse
   │
   ├─ SPA calls /api/v1/* with cookie; middleware resolves
   │  Identity{user, grants} → validates X-Avuru-Tenant against grants
   │
OTLP/Sentry senders ──▶ gateway avuruingestauth ext ──▶ POST hub validate
                        (30s cache, 5m stale grace)     (internal token)
                        key's project stamps tenant → ClickHouse
```

### Storage (ClickHouse, core migration set)

Four tables, ReplacingMergeTree + read-with-FINAL like alert state:

| Table | Contents |
|---|---|
| `auth_users` | id, email, name, bcrypt hash (empty for SSO), origin `local`/`oidc`, disabled |
| `auth_grants` | user_id × scope (project or `*`) × role |
| `auth_sessions` | hashed 256-bit token, user_id, expiries, revoked |
| `auth_ingest_keys` | SHA-256 key hash, project, name, created_by, revoked |

The hub stays stateless — no PVC, no SQLite, no new deps.

### Sessions and request auth

- Login sets an HttpOnly, `SameSite=Lax`, `Secure`-when-TLS cookie; tokens are
  random 256-bit values stored hashed. v1 ships a single absolute TTL
  (default 7d) — strictly tighter than the idle+absolute scheme, which would
  cost a ClickHouse write per request; idle-refresh is deferred.
- Server-side revocation: disabling a user or removing a grant bites on the
  **next request**, not at token expiry (why not JWTs — see Alternatives).
- CSRF: non-GET requests must present a matching `Origin`. Login is
  rate-limited per IP+account. bcrypt cost 12, constant-time compares.
- Middleware order: session (or anonymous synthetic identity) → Identity in
  context → route-level role check → `project(r)` validates the requested
  tenant against grants (403 otherwise). `GET /api/v1/projects` returns only
  granted projects.

### HTTP surface

Unauthenticated: `/healthz` (probes) and `GET /api/v1/auth/config` (which
login methods exist, `forceSSO` — nothing sensitive). Everything else,
including `/api/v1/capabilities`, sits behind the middleware.

New: `POST /auth/login`, `POST /auth/logout`, `GET /auth/me` (identity +
grants + granted projects), `GET /auth/oidc/start` (state+nonce+PKCE),
`GET /auth/oidc/callback`; Admin-only `GET/POST /users`,
`PUT/DELETE /users/{id}` (grants in the payload),
`GET/POST/DELETE /projects/{project}/keys`; internal
`POST /internal/v1/ingest-keys/validate` guarded by a chart-generated
hub↔gateway token.

### UI

Login page driven by `auth/config` (password form and/or SSO button;
`forceSSO` hides the form). The `apiGet/apiPost` wrapper intercepts 401 →
login page — one interception point, the SPA otherwise unchanged. Project
switcher lists granted projects only. New Settings → **Users** (Admin):
create/disable users, assign role-per-project or global Admin. Settings →
project → **API keys** (Admin): create/revoke, one-time secret display. User
menu gains Sign out.

### OIDC configuration (hot-reloadable, like health/alerting)

```yaml
auth:
  oidc:
    issuer: https://keycloak.example.com/realms/avuru
    clientId: avuru-obs
    clientSecretRef: {name: avuru-obs-oidc, key: clientSecret}
    groupsClaim: groups            # e.g. realm_access.roles on Keycloak
    mapping:
      - {group: obs-admins,    role: admin,  projects: ["*"]}
      - {group: team-payments, role: editor, projects: [payments]}
    defaultRole: viewer
    defaultProjects: []            # logged in, sees nothing until granted
    forceSSO: false
```

Mapped grants are recomputed at each SSO login; grants an Admin adds by hand
persist alongside. This — plus IdP federation — is the whole enterprise
identity story (LDAP, AD, SAML upstreams all reach us as OIDC).

### Anonymous demo mode

```yaml
auth:
  anonymous: {enabled: true, role: viewer, projects: [demo]}
```

No-session requests get a synthetic Viewer granted **only** the listed
projects — the public docs demo works credential-free and cannot name-guess
real projects. Logging in replaces the synthetic identity.

### Ingest keys and the gateway extension

`avuruingestauth` (custom extension in the OCB manifest) implements collector
server-auth: reads `Authorization: Bearer` / `X-Avuru-Api-Key`, validates via
the hub's internal endpoint, caches verdicts ~30 s (positive and negative),
serves stale ≤5 min through hub blips, else per `auth.ingest.mode`:

- `off` — extension inert.
- `log` (**default, v0.2**) — accept everything; count and log would-be
  denials (self-metric surfaces in the agents UI).
- `enforce` — reject unkeyed/invalid sends; the key's project stamps the
  tenant, overriding client-supplied `avuru.tenant`.

External senders add the key with standard OTel env
(`OTEL_EXPORTER_OTLP_HEADERS`) — endpoint-only migration still holds until an
install opts into `enforce`.

### Modularity & plugin posture

Auth is **core-with-off-switch**, not a `modules.*` entry: modules gate
optional signal families; auth gates access to everything, and a security
layer togglable like a signal sends the wrong message. It adds **zero cluster
components**. Within auth, each sub-feature is individually inert until
configured (OIDC, anonymous, ingest enforcement).

The project's extension posture, stated so future "plugin" asks have an
anchor (SkyWalking comparison):

- **Tier 1 (now)** — the declared extension points: the in-tree **module
  registry** for feature verticals (error tracking, service health, alerting
  prove the seam); the **OCB manifest** for telemetry receivers/exporters
  (the OTel registry is the plugin ecosystem); **webhooks + the Hub API** for
  outbound and alternative clients; **`auth.Provider` + OIDC federation** for
  identity. SkyWalking's OAP "modules with selectable providers" maps to
  exactly this; its runtime-loaded agent jars have no safe Go equivalent.
- **Tier 2 (future AEP, when a concrete third-party feature wants in)** —
  **companion-service plugins**: a plugin is its own Deployment/chart that
  registers into `/api/v1/capabilities` over a versioned REST contract; the
  UI shows/hides its entry; a bad plugin crashes its own pod. Out-of-process
  binary plugins (HashiCorp go-plugin/Grafana model) and WASM transform hooks
  (wazero) are explicitly deferred with those trigger conditions.

### Alternatives considered

- **JWT/bearer everywhere (SigNoz-style)** — stateless, but revocation goes
  weak ("remove user from project" must mean *now*), anonymous mode gets
  awkward, and the SPA grows token-refresh machinery for no gain single-origin.
- **oauth2-proxy at the ingress (Jaeger-style)** — no local users (breaks the
  wedge), still needs hub-side RBAC anyway, and keeps header trust — the
  pattern this AEP removes.
- **Bundled Dex sub-chart** — identity without hub code, but adds a component
  to operate and secure; unnecessary since local+OIDC covers it and Dex can
  still be *pointed at* as an IdP.
- **SQLite/Postgres for users** (Coroot/Grafana do this) — would make the hub
  stateful (PVC) or add a dependency; ClickHouse is already there and the
  write rates are trivial.
- **Gateway validates keys against ClickHouse directly** — avoids the hub
  round-trip but adds a CH client + schema coupling to the collector distro;
  the cached control-plane call keeps the contract in one place.
- **`auth.ingest.mode: enforce` by default** — secure-by-default extended to
  ingest, rejected for v0.2: upgrades would silently drop telemetry from
  every existing external sender, denting the drop-in promise.

## Verification

- **Unit**: authz matrix (route × role × grant scope), grant resolution,
  OIDC group mapping, session expiry/revocation, Origin/CSRF check, login
  rate limiting, key hash verify.
- **Integration** (ClickHouse): user/grant/session/key stores, FINAL
  semantics on replace/revoke.
- **Gateway extension**: fake-hub tests — cache TTL, negative cache, stale
  grace, `log` vs `enforce`, tenant stamping.
- **e2e (compose)**: the isolation story — user granted `project-b` gets 403
  and an empty switcher for `project-a`; anonymous sees only listed projects;
  admin bootstrap; login/logout.
- **Playwright**: login page (local + error states), Settings→Users CRUD,
  key create with one-time display, 401 → login redirect.
- **TTV gate**: sets the admin password via Helm, logs in, asserts the
  service map — the 5-minute promise now includes auth.
- **Docs**: `docs-align` run (feature pages, changelog EN/FR); demo instance
  reconfigured with anonymous mode + `demo` project.

Done = an upgraded LAN instance shows a login screen to strangers, the docs
demo still works publicly showing only `demo`, a second user sees only their
granted project, and flipping `enforce` rejects unkeyed OTLP.

## Roadmap

- [ ] AEP accepted
- [ ] `hub/internal/auth`: stores, sessions, middleware, local provider,
      bootstrap admin
- [ ] RBAC enforcement on existing routes + granted-projects filtering
- [ ] Auth HTTP surface + login UI + 401 interception + Settings→Users
- [ ] Anonymous demo mode
- [ ] OIDC provider + group mapping (+ Keycloak e2e against a real realm)
- [ ] Ingest keys: store, admin UI, static chart keys, sensor key wiring
- [ ] Gateway `avuruingestauth` extension + `mode: off|log|enforce`
- [ ] Helm: secrets (admin, internal token, sensor key), values, upgrade notes
- [ ] Wedge/TTV gate + e2e + Playwright green
- [ ] docs-align (EN/FR) + demo instance flipped to anonymous-scoped
