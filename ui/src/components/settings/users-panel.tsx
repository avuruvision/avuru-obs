"use client";

import { Fragment, useCallback, useEffect, useState } from "react";
import { Plus, TriangleAlert, X } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { apiDelete, apiGet, apiPost, apiPut, ApiError } from "@/lib/api";
import type {
  AdminUser,
  AuthGrant,
  CreateUserRequest,
  UpdateUserRequest,
  UsersResponse,
} from "@/lib/api-types";

const ROLES: AuthGrant["role"][] = ["viewer", "editor", "admin"];

const inputClass =
  "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

// Grants render as `role@scope` (e.g. `admin@*`), joined — or an em dash when a
// user carries none. Mirrors how the rest of the app shows compound values.
function grantsLabel(grants: AuthGrant[]): string {
  if (grants.length === 0) return "—";
  return grants.map((g) => `${g.role}@${g.scope}`).join(", ");
}

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

// Mirrors validGrants in hub/internal/api/users.go so the obvious mistakes
// answer instantly instead of round-tripping to a 400. The hub still
// re-validates — this is convenience, not the enforcement point.
function grantsError(grants: AuthGrant[]): string | null {
  const seen = new Set<string>();
  for (const g of grants) {
    if (g.scope === "") return "Every grant needs a project scope (* for all).";
    if (seen.has(g.scope)) return `Duplicate grant scope “${g.scope}”.`;
    seen.add(g.scope);
  }
  return null;
}

// Which inline panel a row has open. Only one at a time, and opening one closes
// the others — the panels edit overlapping state (grants vs password) and a
// half-filled password form left open under an expanded editor reads as if it
// would be saved along with it.
type RowPanel = "edit" | "password" | "delete";

// Local-user administration (admin only — the hub answers 403 otherwise, which
// surfaces as the load error). Create users, edit name and grants, reset or
// disable them, and delete the ones already disabled. Modeled on the alert
// ChannelsPanel: a Card with a table and inline forms.
export function UsersPanel() {
  const [users, setUsers] = useState<AdminUser[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  // All state updates land in the async callbacks so this stays safe to call
  // straight from the mount effect (no synchronous setState in the effect body).
  const reload = useCallback(() => {
    apiGet<UsersResponse>("/api/v1/users")
      .then((r) => {
        setUsers(r.users);
        setLoadError(null);
      })
      .catch((err) => {
        setUsers([]);
        setLoadError(err instanceof ApiError ? err.message : "request failed");
      });
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  const createUser = async (req: CreateUserRequest) => {
    // Let ApiError propagate to the form so it renders the field-level message.
    await apiPost<AdminUser>("/api/v1/users", req);
    setAdding(false);
    reload();
  };

  if (users === null) return <CenteredSpinner />;

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Users</CardTitle>
        {!adding && (
          <Button variant="secondary" size="sm" onClick={() => setAdding(true)}>
            <Plus className="h-3.5 w-3.5" /> Add user
          </Button>
        )}
      </CardHeader>

      {loadError && (
        <p className="border-t border-neutral px-4 py-3 text-sm text-error">
          Couldn’t load users: {loadError}
        </p>
      )}

      {!loadError && users.length === 0 && !adding ? (
        <p className="px-4 pb-4 text-sm text-base-content/55">
          No users yet. Add one to grant access to this workspace.
        </p>
      ) : users.length > 0 ? (
        <div className="overflow-x-auto border-t border-neutral">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs uppercase tracking-wider text-base-content/40">
                <th className="px-4 py-2 font-semibold">Email</th>
                <th className="px-4 py-2 font-semibold">Name</th>
                <th className="px-4 py-2 font-semibold">Grants</th>
                <th className="px-4 py-2 font-semibold">Status</th>
                <th className="px-4 py-2 text-right font-semibold">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-neutral">
              {users.map((u) => (
                <UserRow key={u.id} u={u} onChanged={reload} />
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {adding && <AddUserForm onSubmit={createUser} onCancel={() => setAdding(false)} />}
    </Card>
  );
}

// UserRow owns its own busy/error/panel state so one row's failed save never
// blanks another row's form. The expanded panel is a second <tr> spanning the
// table — the edit and delete copy need more room than the actions cell has.
function UserRow({ u, onChanged }: { u: AdminUser; onChanged: () => void }) {
  const [panel, setPanel] = useState<RowPanel | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const toggle = (p: RowPanel) => {
    setError(null);
    setPanel((cur) => (cur === p ? null : p));
  };

  // save/remove funnel every write through one busy+error path; on success the
  // panel closes and the parent reloads (the hub's response is authoritative —
  // grants come back normalized, so re-rendering from a local guess would drift).
  const save = async (req: UpdateUserRequest) => {
    setError(null);
    setBusy(true);
    try {
      await apiPut<AdminUser>(`/api/v1/users/${u.id}`, req);
      setPanel(null);
      onChanged();
    } catch (e) {
      setError(errMessage(e, "request failed"));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setError(null);
    setBusy(true);
    try {
      await apiDelete(`/api/v1/users/${u.id}`);
      setPanel(null);
      onChanged();
    } catch (e) {
      setError(errMessage(e, "request failed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Fragment>
      <tr data-testid={`user-row-${u.email}`}>
        <td className="px-4 py-2.5 font-mono">{u.email}</td>
        <td className="px-4 py-2.5">{u.name || "—"}</td>
        <td className="px-4 py-2.5 text-base-content/70">{grantsLabel(u.grants)}</td>
        <td className="px-4 py-2.5">
          <span className={u.disabled ? "text-base-content/45" : "text-success"}>
            {u.disabled ? "disabled" : "active"}
          </span>
          {u.origin === "oidc" && (
            <span className="ml-1.5 text-xs text-base-content/45">· SSO</span>
          )}
        </td>
        <td className="px-4 py-2.5 text-right">
          <div className="flex justify-end gap-1">
            <Button variant="ghost" size="sm" onClick={() => toggle("edit")} disabled={busy}>
              Edit
            </Button>
            {/* An SSO user's credential lives at the IdP — the hub answers 400
                on a password for origin != local, so don't offer the form. */}
            {u.origin === "local" && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => toggle("password")}
                disabled={busy}
              >
                Reset password
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void save({ disabled: !u.disabled })}
              disabled={busy}
            >
              {u.disabled ? "Enable" : "Disable"}
            </Button>
            {/* Delete is offered only once the user is already disabled: the hub
                answers 409 otherwise, and the disable step is the recovery
                window that makes a hard delete safe to offer at all. */}
            {u.disabled && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => toggle("delete")}
                disabled={busy}
              >
                Delete
              </Button>
            )}
          </div>
        </td>
      </tr>

      {(panel || error) && (
        <tr>
          <td colSpan={5} className="bg-base-200/40 px-4 py-3">
            {panel === "edit" && (
              <EditUserForm
                u={u}
                busy={busy}
                onSubmit={save}
                onCancel={() => setPanel(null)}
              />
            )}
            {panel === "password" && (
              <ResetPasswordForm
                email={u.email}
                busy={busy}
                onSubmit={(password) => save({ password })}
                onCancel={() => setPanel(null)}
              />
            )}
            {panel === "delete" && (
              <DeleteConfirm
                u={u}
                busy={busy}
                onConfirm={remove}
                onCancel={() => setPanel(null)}
              />
            )}
            {error && (
              <p className="mt-2 text-xs text-error" data-testid="user-row-error">
                {error}
              </p>
            )}
          </td>
        </tr>
      )}
    </Fragment>
  );
}

// Inline editor for name + the whole grant set. Grants are sent as a complete
// replacement (the hub's PUT semantics), so what's on screen when you save is
// exactly what the user ends up with — including an empty list.
function EditUserForm({
  u,
  busy,
  onSubmit,
  onCancel,
}: {
  u: AdminUser;
  busy: boolean;
  onSubmit: (req: UpdateUserRequest) => Promise<void>;
  onCancel: () => void;
}) {
  const [name, setName] = useState(u.name);
  const [grants, setGrants] = useState<AuthGrant[]>(u.grants);

  const invalid = grantsError(grants);

  const setGrant = (i: number, patch: Partial<AuthGrant>) =>
    setGrants((gs) => gs.map((g, j) => (j === i ? { ...g, ...patch } : g)));

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (invalid) return;
    void onSubmit({ name, grants });
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <label className="flex max-w-sm flex-col gap-1 text-xs text-base-content/60">
        Name
        <input
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Jane Doe"
        />
      </label>

      <div className="flex flex-col gap-1.5">
        <span className="text-xs text-base-content/60">Grants</span>
        {grants.length === 0 && (
          <p className="text-xs text-base-content/45">
            No grants — this user can sign in but sees nothing.
          </p>
        )}
        {grants.map((g, i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              className={`${inputClass} max-w-56`}
              value={g.scope}
              onChange={(e) => setGrant(i, { scope: e.target.value })}
              placeholder="* or project"
              aria-label="Project scope"
            />
            <select
              className={`${inputClass} max-w-32`}
              value={g.role}
              onChange={(e) => setGrant(i, { role: e.target.value as AuthGrant["role"] })}
              aria-label="Role"
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={`Remove grant ${i + 1}`}
              onClick={() => setGrants((gs) => gs.filter((_, j) => j !== i))}
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setGrants((gs) => [...gs, { scope: "", role: "viewer" }])}
          >
            <Plus className="h-3.5 w-3.5" /> Add grant
          </Button>
        </div>
      </div>

      {invalid && <p className="text-xs text-error">{invalid}</p>}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={busy || invalid !== null}>
          Save changes
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

// Admin-side password reset. Rotating a password revokes that user's sessions
// server-side, so the warning is a statement of what already happens — not a
// choice offered here.
function ResetPasswordForm({
  email,
  busy,
  onSubmit,
  onCancel,
}: {
  email: string;
  busy: boolean;
  onSubmit: (password: string) => Promise<void>;
  onCancel: () => void;
}) {
  const [password, setPassword] = useState("");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (password === "") return;
    void onSubmit(password);
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <label className="flex max-w-sm flex-col gap-1 text-xs text-base-content/60">
        New password for <span className="font-mono">{email}</span>
        <input
          className={inputClass}
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="••••••••"
          required
        />
      </label>
      <p className="text-xs text-base-content/50">
        Signs this user out everywhere — their existing sessions are revoked.
      </p>
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={busy || password === ""}>
          Set password
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

// Two-click confirm, same shape as the ingest-key revoke. The copy differs by
// origin because the OUTCOME differs: for a local user this destroys the
// credential, but for an SSO user it deletes only the local record — and
// `disabled` is the flag the SSO callback checks, so deleting a disabled SSO
// user REMOVES their lockout and they return on their next IdP login.
function DeleteConfirm({
  u,
  busy,
  onConfirm,
  onCancel,
}: {
  u: AdminUser;
  busy: boolean;
  onConfirm: () => Promise<void>;
  onCancel: () => void;
}) {
  const sso = u.origin === "oidc";
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-warning/50 bg-warning/10 p-3">
      <p className="flex items-start gap-2 text-xs text-base-content/80">
        <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" aria-hidden />
        {sso ? (
          <span data-testid="delete-warning-sso">
            <span className="font-mono">{u.email}</span> signs in through your
            identity provider. Deleting removes only the local record and their
            grants — <strong>it undoes this lockout</strong>: they can sign in
            again on their next SSO login, with group-mapped grants only. Keep
            them disabled to keep them out.
          </span>
        ) : (
          <span data-testid="delete-warning-local">
            Permanently delete <span className="font-mono">{u.email}</span> and
            their grants. Their sessions are revoked and the account cannot be
            recovered — you would have to create it again.
          </span>
        )}
      </p>
      <div className="flex items-center gap-2">
        <Button type="button" variant="danger" size="sm" onClick={() => void onConfirm()} disabled={busy}>
          Delete permanently
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

// Inline create form: email + name + password, plus an optional single grant
// (project scope + role). An empty scope creates a user with no grants.
function AddUserForm({
  onSubmit,
  onCancel,
}: {
  onSubmit: (req: CreateUserRequest) => Promise<unknown>;
  onCancel: () => void;
}) {
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [scope, setScope] = useState("");
  const [role, setRole] = useState<AuthGrant["role"]>("viewer");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await onSubmit({
        email,
        name,
        password,
        grants: scope ? [{ scope, role }] : [],
      });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "request failed");
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t border-neutral px-4 py-3">
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Email
          <input
            className={inputClass}
            type="email"
            autoComplete="off"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
            required
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Name
          <input
            className={inputClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Jane Doe"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Password
          <input
            className={inputClass}
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="••••••••"
            required
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Project scope
          <input
            className={inputClass}
            value={scope}
            onChange={(e) => setScope(e.target.value)}
            placeholder="* or project (blank = no grant)"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Role
          <select
            className={inputClass}
            value={role}
            onChange={(e) => setRole(e.target.value as AuthGrant["role"])}
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        </label>
      </div>
      {error && <p className="text-xs text-error">{error}</p>}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={busy}>
          Add user
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
