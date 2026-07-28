"use client";

import { useCallback, useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { apiGet, apiPost, apiPut, ApiError } from "@/lib/api";
import type {
  AdminUser,
  AuthGrant,
  CreateUserRequest,
  UsersResponse,
} from "@/lib/api-types";

const ROLES: AuthGrant["role"][] = ["viewer", "editor", "admin"];

// Grants render as `role@scope` (e.g. `admin@*`), joined — or an em dash when a
// user carries none. Mirrors how the rest of the app shows compound values.
function grantsLabel(grants: AuthGrant[]): string {
  if (grants.length === 0) return "—";
  return grants.map((g) => `${g.role}@${g.scope}`).join(", ");
}

// Local-user administration (admin only — the hub answers 403 otherwise, which
// surfaces as the load error). Create users, grant one role per project scope,
// and enable/disable. Modeled on the alert ChannelsPanel: a Card with a table
// and an inline add form.
export function UsersPanel() {
  const [users, setUsers] = useState<AdminUser[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);

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

  const toggleDisabled = async (u: AdminUser) => {
    setActionError(null);
    setBusyId(u.id);
    try {
      await apiPut(`/api/v1/users/${u.id}`, { disabled: !u.disabled });
      reload();
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "request failed");
    } finally {
      setBusyId(null);
    }
  };

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
                <tr key={u.id}>
                  <td className="px-4 py-2.5 font-mono">{u.email}</td>
                  <td className="px-4 py-2.5">{u.name || "—"}</td>
                  <td className="px-4 py-2.5 text-base-content/70">
                    {grantsLabel(u.grants)}
                  </td>
                  <td className="px-4 py-2.5">
                    <span className={u.disabled ? "text-base-content/45" : "text-success"}>
                      {u.disabled ? "disabled" : "active"}
                    </span>
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => void toggleDisabled(u)}
                      disabled={busyId === u.id}
                    >
                      {u.disabled ? "Enable" : "Disable"}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {actionError && (
        <p className="border-t border-neutral px-4 py-2 text-xs text-error">
          {actionError}
        </p>
      )}

      {adding && <AddUserForm onSubmit={createUser} onCancel={() => setAdding(false)} />}
    </Card>
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

  const inputClass =
    "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

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
