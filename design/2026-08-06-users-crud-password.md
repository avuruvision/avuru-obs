# AEP: Users CRUD completion — delete, password management, role editing

- **Date:** 2026-08-06
- **Author(s):** Berny ryders
- **Status:** Accepted (amends the disable-not-delete decision in
  [2026-07-21-auth-oidc-rbac.md](./2026-07-21-auth-oidc-rbac.md))

## Summary

Round out local user management for v0.4: admins can **edit** a user's name,
roles (grants) and password from the Users panel, and **delete** a user that
has first been disabled; any signed-in local user can **change their own
password** from a new Settings → Account tab. The hub API already carries most
of the update surface (`PUT /api/v1/users/{id}` edits name/password/disabled/
grants with self-lockout guards) — the bulk of this AEP is the missing UI, one
new `DELETE` route behind a disabled-first rule, and one new self-service
password endpoint.

## Motivation

v0.2/v0.3 shipped secure-by-default auth (Plan A local users + Plan B OIDC),
but the Users panel only exposes list, create and enable/disable. An admin who
mistypes a name, needs to rotate a compromised password, or wants to widen a
viewer to editor must call the API by hand; a user who wants to change their
own password cannot at all; and test users accumulate forever because the AEP
deliberately chose disable-not-delete. Operationally, "manage users without
curl" is table stakes for the self-hosted wedge ([AGENTS.md](../AGENTS.md)),
and none of it touches the telemetry byte-path (the hub stays a control plane,
per the [locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale)).

### Goals

- **Delete, behind a two-step:** `DELETE /api/v1/users/{id}` succeeds only for
  a user that is **already disabled** (409 otherwise). Disabling stays the
  reversible first step; delete is the explicit cleanup. Self-delete is
  structurally impossible: a signed-in admin cannot be disabled (self-lockout
  guard), and a disabled user has no live session.
- **Admin edit in the UI:** inline per-row edit form (same pattern as the
  existing add-user form) for name and the full grant set — multiple
  scope+role rows, scope = `*` or a project id, role = admin/editor/viewer —
  plus an admin "reset password" action (hidden for OIDC users).
- **Self-service password change:** `POST /api/v1/auth/password` for any
  authenticated non-anonymous **local** user, requiring the current password.
  On success every session is revoked and a fresh one is minted in the same
  response — a stolen cookie dies, the legitimate user stays signed in.
- **Origin honesty:** password operations (admin reset and self-service) are
  rejected for `origin=oidc` users — their credential lives at the IdP.
  `PUT /api/v1/users/{id}` gains the same guard (today it would silently
  store a useless hash).

### Non-goals

- **No GDPR-style erasure pipeline.** Delete tombstones the auth record; it
  does not scrub the user's email from audit log lines already emitted.
- **No role model changes.** Roles stay admin/editor/viewer; scopes stay
  `*`/project. No per-signal or custom roles.
- **No SSO deprovisioning.** Deleting an `origin=oidc` user removes the local
  record (manual grants included); if the IdP still authenticates them they
  return as a fresh user with group-mapped grants only. That is the correct
  boundary: the IdP owns their existence.
- **No password policy / expiry / strength meter** beyond the existing
  `auth.HashPassword` validation.

## Solution

**Storage** (migration `0015`): `auth_user` gains `Deleted UInt8 DEFAULT 0`
(the column 0010 deliberately omitted). Reads filter `Deleted = 0`;
`DeleteAuthUser` tombstones via the same ReplacingMergeTree supersede as
`DeleteProject`. Grants already tombstone (`ReplaceAuthGrants` with an empty
set); sessions already revoke (`RevokeAuthSessionsForUser`).

**API** (`hub/internal/api/users.go`, `auth_handlers.go`; all under the
existing middleware):

- `DELETE /api/v1/users/{id}` (securedAdmin) — 404 unknown, **409 unless
  `Disabled`**, else: revoke sessions → clear grants → tombstone user, in that
  order (a crash mid-sequence leaves a disabled, grantless user — recoverable,
  never a half-deleted one that can still sign in).
- `POST /api/v1/auth/password` (authenticated) — body
  `{currentPassword, newPassword}`. Guards, in order: non-anonymous identity;
  `origin=local` (400 with "managed by your identity provider" otherwise);
  not the shared demo account (403 — a public demo must not be re-keyed by a
  visitor); current password verifies under the login rate-limiter (a stolen
  session must not become a password-guessing oracle); new password passes
  `HashPassword` validation. On success: save hash, revoke **all** sessions,
  mint a fresh session and set the cookie.
- `PUT /api/v1/users/{id}` — reject `password` for `origin!=local` (400).
  Everything else (name/disabled/grants, self-lockout, session revocation on
  rotation) is already in place and unchanged.

**UI** (`ui/src/components/settings/`):

- **Users panel:** each row gains Edit (inline expanding form: name + grant
  rows with add/remove, duplicate-scope validation client-side), Reset
  password (inline, admin types the new password; hidden for `origin=oidc`),
  the existing Enable/Disable, and Delete — rendered **only on disabled
  rows**, two-click confirm (same pattern as the collection-control reset).
  Hub 400s (self-lockout, bad grants) surface inline.
- **Account tab:** new Settings tab, visible to any signed-in non-anonymous
  user. Shows email/name/origin; local users get the change-password form
  (current + new + confirm, client-side match check); OIDC users see the
  IdP note instead. Success shows a confirmation (the session was re-minted
  server-side, so no re-login).

The SPA keeps talking to the hub's REST API only (enterprise seam intact); no
new capability flag is needed — the routes exist whenever auth is enabled,
and the tab/actions render from `/auth/me` state the SPA already has.

### Alternatives considered

- **Hard delete on any user (no disabled-first rule)** — one confirm click
  from destroying the identity behind historical audit lines; rejected for a
  two-step that keeps a recovery window.
- **Keep disable-only (original Plan A)** — leaves test/offboarded users
  accumulating forever in a panel that cannot shrink; rejected now that the
  panel is writable anyway.
- **Soft-delete flag without a migration (reuse `Disabled`)** — conflates two
  states the UI must distinguish (recoverable vs gone) and makes "re-enable"
  ambiguous; rejected.
- **Forced re-login after self-service password change** — simpler (no
  re-mint), but punishes the legitimate user for rotating their own
  credential; rejected since `RevokeAuthSessionsForUser` + mint is two calls.
- **Modal dialogs for edit** — a new UI primitive this codebase doesn't have;
  inline expanding forms match the existing add-user pattern.

## Verification

- **Unit (hub):** delete guards (404 / 409-not-disabled / tombstone visible in
  list), PUT origin guard, `/auth/password` matrix (wrong current password,
  OIDC user, demo user, anonymous, rate-limited, success rotates sessions and
  keeps the caller signed in).
- **Integration (ClickHouse):** `DeleteAuthUser` tombstone supersedes under
  FINAL; deleted user invisible to `ListAuthUsers`/`GetAuthUserByEmail`; a
  re-created same-email user is a fresh row.
- **e2e (Playwright, route-stubbed):** users panel edit name/roles round-trip,
  reset password, delete only offered on disabled rows, self-lockout error
  surfaces; Account tab change-password happy path + OIDC note.
- **Done** = an admin can create, read, edit (name/roles/password), disable
  and delete a user entirely from Settings → Users; a local user can change
  their own password from Settings → Account; both flows behave per the
  guards above with no `curl` required.

## Roadmap

- [ ] AEP accepted
- [ ] Migration `0015` + `DeleteAuthUser` (ClickHouse + storagetest fake)
- [ ] `DELETE /api/v1/users/{id}` + PUT origin guard + tests
- [ ] `POST /api/v1/auth/password` + rate-limit reuse + tests
- [ ] Users panel: inline edit (name/grants), reset password, delete-on-disabled
- [ ] Settings → Account tab (self-service password change)
- [ ] Playwright specs (users + account) · changelog · docs-align (EN/FR)
