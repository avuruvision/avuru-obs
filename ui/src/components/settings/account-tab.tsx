"use client";

import { useState } from "react";
import { ShieldCheck } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/hooks/use-auth";
import { apiPost, ApiError } from "@/lib/api";
import type { ChangePasswordRequest, Me } from "@/lib/api-types";

const inputClass =
  "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

// How the identity signed in. Origin is a plain string on the wire, so an
// origin this build doesn't know about renders as itself rather than being
// mislabelled as one of the two we do know.
function originLabel(origin: string): string {
  switch (origin) {
    case "local":
      return "Password (local account)";
    case "oidc":
      return "Single sign-on";
    default:
      return origin || "—";
  }
}

// Settings → Account: the signed-in user's own identity and, where it applies,
// self-service password rotation. Rendered for any non-anonymous session
// regardless of grants — a viewer with no grants at all still owns their
// credential, and nothing here is admin-gated (that's the Users tab).
//
// Being read-only is NOT what removes the form. The one local account that
// cannot rotate its own password is the shared demo viewer: the hub refuses it
// by server-reserved id because that row is re-keyed from the install's
// configuration on every boot and the credential is shared. `origin` can't see
// that — the demo viewer is an ordinary local user — so the hub decides and
// says which case applies in me.user.passwordChange.
export function AccountTab() {
  const { me } = useAuth();

  // Unknown identity (auth off, or the hub is unreachable) and the anonymous
  // fallback both have no account to show — the tab isn't offered in either
  // case, so this is the defensive half of the same gate.
  if (!me || me.user.anonymous) {
    return (
      <p className="text-sm text-base-content/55">
        No signed-in account. <a className="underline" href="/login">Sign in</a> to
        manage your credentials.
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Account</CardTitle>
          <span className="text-xs text-base-content/50">your identity on this instance</span>
        </CardHeader>
        <dl className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-3">
          <div className="bg-base-200 p-3">
            <dt className="text-xs uppercase tracking-wider text-base-content/50">Email</dt>
            <dd className="truncate font-mono text-sm" data-testid="account-email">
              {me.user.email || "—"}
            </dd>
          </div>
          <div className="bg-base-200 p-3">
            <dt className="text-xs uppercase tracking-wider text-base-content/50">Name</dt>
            <dd className="truncate text-sm">{me.user.name || "—"}</dd>
          </div>
          <div className="bg-base-200 p-3">
            <dt className="text-xs uppercase tracking-wider text-base-content/50">Sign-in</dt>
            <dd className="text-sm" data-testid="account-origin">
              {originLabel(me.user.origin)}
            </dd>
          </div>
        </dl>
      </Card>

      {me.user.passwordChange === "self" ? (
        <ChangePasswordCard />
      ) : (
        <PasswordNote kind={me.user.passwordChange} />
      )}
    </div>
  );
}

// Why there is no password form. Every value other than "self" lands here,
// including one this build doesn't recognise: the fallback copy is true of any
// refusal, which is what makes defaulting to the note — never to the form —
// safe. Offering a form the hub will reject is the failure this replaces; the
// demo viewer used to get one, fill it in, and collect a 403.
function PasswordNote({ kind }: { kind: string }) {
  const { testId, text } =
    kind === "shared"
      ? {
          testId: "account-shared-note",
          text: "This is the shared read-only demo account. Its password is managed by the server and re-applied from the install's configuration on every restart, so it can't be changed here.",
        }
      : kind === "idp"
        ? {
            testId: "account-idp-note",
            text: "Your password is managed by your identity provider. Change it there — this instance never stores it.",
          }
        : {
            testId: "account-no-password-note",
            text: "This account's password isn't managed from this screen.",
          };

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Password</CardTitle>
      </CardHeader>
      <p
        className="flex items-start gap-2 border-t border-neutral p-4 text-sm text-base-content/70"
        data-testid={testId}
      >
        <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-base-content/50" aria-hidden />
        {text}
      </p>
    </Card>
  );
}

// Self-service rotation. The hub verifies the current password under the login
// rate-limiter, then revokes every session and mints a fresh one on this
// response — so a success leaves the user signed in HERE and signed out
// everywhere else, which is what the confirmation says.
function ChangePasswordCard() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  // Checked here only to spare a round-trip; the hub has no idea the user typed
  // it twice, so this is the sole place the mismatch can be caught.
  const mismatch = confirm !== "" && next !== confirm;
  const ready = current !== "" && next !== "" && confirm !== "" && !mismatch;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    setError(null);
    setDone(false);
    setBusy(true);
    try {
      const body: ChangePasswordRequest = { currentPassword: current, newPassword: next };
      await apiPost<Me>("/api/v1/auth/password", body);
      // Clear the fields on success: leaving a filled-in password form behind a
      // success banner invites a second submit that would now fail on a stale
      // "current".
      setCurrent("");
      setNext("");
      setConfirm("");
      setDone(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "request failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Change password</CardTitle>
      </CardHeader>
      <form onSubmit={submit} className="flex flex-col gap-3 border-t border-neutral p-4">
        <div className="grid max-w-2xl grid-cols-1 gap-2 sm:grid-cols-3">
          <label className="flex flex-col gap-1 text-xs text-base-content/60">
            Current password
            <input
              className={inputClass}
              type="password"
              autoComplete="current-password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              placeholder="••••••••"
              required
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-base-content/60">
            New password
            <input
              className={inputClass}
              type="password"
              autoComplete="new-password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
              placeholder="••••••••"
              required
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-base-content/60">
            Confirm new password
            <input
              className={inputClass}
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              placeholder="••••••••"
              required
            />
          </label>
        </div>

        {mismatch && (
          <p className="text-xs text-error" data-testid="password-mismatch">
            The new passwords don’t match.
          </p>
        )}
        {error && (
          <p className="text-xs text-error" data-testid="password-error">
            {error}
          </p>
        )}
        {done && (
          <p className="text-xs text-success" data-testid="password-changed">
            Password changed. You stay signed in here; every other session was
            signed out.
          </p>
        )}

        <div>
          <Button type="submit" variant="primary" size="sm" disabled={busy || !ready}>
            Change password
          </Button>
        </div>
      </form>
    </Card>
  );
}
