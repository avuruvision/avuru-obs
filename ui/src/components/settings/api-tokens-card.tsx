"use client";

import { useState } from "react";
import { KeySquare, TriangleAlert } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Select } from "@/components/ui/select";
import {
  useApiTokens,
  useCreateApiToken,
  useRevokeApiToken,
} from "@/hooks/use-api-tokens";
import { ApiError } from "@/lib/api";
import type { ApiToken, CreateApiTokenResponse } from "@/lib/api-types";

const inputClass =
  "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

// "Never" is a real expiry choice, not a missing one — the hub reads
// expiresInDays 0/absent as no expiry.
const EXPIRY_OPTIONS = [
  { value: "0", label: "never expires" },
  { value: "30", label: "expires in 30 days" },
  { value: "90", label: "expires in 90 days" },
  { value: "365", label: "expires in a year" },
];

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

function fmtDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString();
}

function isExpired(t: ApiToken): boolean {
  return !!t.expiresAt && new Date(t.expiresAt).getTime() < Date.now();
}

// ApiTokensCard manages the CALLER's personal API tokens: list (prefix +
// metadata), create (raw secret shown once), revoke. Copies the shape of
// IngestKeysCard — same one-time disclosure, same confirmed revoke — but is
// deliberately NOT admin-gated: the hub's routes are `authenticated`, every
// signed-in user owns their own tokens. The Access tab leaves the card out
// when auth is disabled — with no identity there is nothing to own a token.
export function ApiTokensCard() {
  const { data, isLoading } = useApiTokens(true);

  const tokens = data?.tokens ?? [];

  return (
    <Card className="overflow-hidden" data-testid="api-tokens-card">
      <CardHeader>
        <CardTitle>
          <span className="inline-flex items-center gap-2">
            <KeySquare className="h-4 w-4 text-base-content/60" aria-hidden />
            API tokens
          </span>
        </CardTitle>
        <span className="text-xs text-base-content/50">
          scripts and CI against the Hub API
        </span>
      </CardHeader>

      <div className="flex flex-col gap-3 border-t border-neutral p-4">
        <p className="text-xs text-base-content/60">
          A token is sent as{" "}
          <span className="font-mono">Authorization: Bearer …</span> and acts
          with <strong>your current permissions</strong>, read live on every
          request — handing one to a CI job is handing over your own reach, and
          narrowing your role narrows every token you hold with it.
        </p>

        {isLoading ? (
          <p className="text-xs text-base-content/45">Loading tokens…</p>
        ) : tokens.length === 0 ? (
          <p className="text-xs text-base-content/45" data-testid="api-tokens-empty">
            No tokens yet. Create one below for non-interactive access.
          </p>
        ) : (
          <ul
            className="flex flex-col divide-y divide-neutral rounded-lg border border-neutral"
            data-testid="api-tokens-list"
          >
            {tokens.map((t) => (
              <TokenRow key={t.tokenHash} t={t} />
            ))}
          </ul>
        )}

        <CreateTokenForm />
      </div>
    </Card>
  );
}

function TokenRow({ t }: { t: ApiToken }) {
  const revoke = useRevokeApiToken();
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const expired = isExpired(t);

  async function remove() {
    setError(null);
    try {
      await revoke.mutateAsync(t.tokenHash);
    } catch (e) {
      setError(errMessage(e, "Failed to revoke token"));
      setConfirming(false);
    }
  }

  return (
    <li className="flex flex-wrap items-center justify-between gap-2 p-3">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{t.name}</span>
          {expired && (
            <Badge tone="error" title="Expired tokens stay listed so you can see why a script stopped working">
              expired
            </Badge>
          )}
        </span>
        <span className="text-xs text-base-content/50">
          <span className="font-mono">{t.prefix}…</span> · created{" "}
          {fmtDate(t.createdAt)} ·{" "}
          {t.lastUsedAt ? `last used ${fmtDate(t.lastUsedAt)}` : "never used"} ·{" "}
          {t.expiresAt
            ? `${expired ? "expired" : "expires"} ${fmtDate(t.expiresAt)}`
            : "never expires"}
        </span>
        {error && <span className="text-xs text-error">{error}</span>}
      </div>
      {confirming ? (
        <div className="flex gap-2">
          <Button
            type="button"
            variant="danger"
            size="sm"
            onClick={remove}
            disabled={revoke.isPending}
            data-testid={`confirm-revoke-api-token-${t.name}`}
          >
            Revoke
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setConfirming(false)}
          >
            Cancel
          </Button>
        </div>
      ) : (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setConfirming(true)}
          data-testid={`revoke-api-token-${t.name}`}
        >
          Revoke
        </Button>
      )}
    </li>
  );
}

function CreateTokenForm() {
  const create = useCreateApiToken();
  const [name, setName] = useState("");
  const [expiry, setExpiry] = useState("0");
  const [error, setError] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<CreateApiTokenResponse | null>(null);

  async function submit() {
    setError(null);
    try {
      const res = await create.mutateAsync({
        name: name.trim(),
        expiresInDays: Number(expiry),
      });
      setRevealed(res);
      setName("");
      setExpiry("0");
    } catch (e) {
      setError(errMessage(e, "Failed to create token"));
    }
  }

  if (revealed) {
    return <RevealPanel created={revealed} onDone={() => setRevealed(null)} />;
  }

  return (
    <div className="flex flex-col gap-2 border-t border-neutral pt-3">
      <label className="flex flex-col gap-1 text-sm">
        New token name
        <div className="flex flex-wrap gap-2">
          <input
            className={`${inputClass} max-w-64`}
            value={name}
            maxLength={200}
            placeholder="ci-deploy"
            onChange={(e) => setName(e.target.value)}
            data-testid="api-token-name"
          />
          <Select
            value={expiry}
            options={EXPIRY_OPTIONS}
            onChange={setExpiry}
            ariaLabel="Token expiry"
            className="h-8"
          />
          <Button
            type="button"
            variant="primary"
            size="sm"
            onClick={submit}
            disabled={create.isPending || name.trim() === ""}
            data-testid="create-api-token"
          >
            Create token
          </Button>
        </div>
      </label>
      {error && <p className="text-xs text-error">{error}</p>}
    </div>
  );
}

// RevealPanel shows the raw token ONCE. There is no way to see it again — the
// hub stores only its hash — so the copy affordance and the warning are
// prominent.
function RevealPanel({
  created,
  onDone,
}: {
  created: CreateApiTokenResponse;
  onDone: () => void;
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-warning/50 bg-warning/10 p-3">
      <p className="flex items-start gap-2 text-xs text-base-content/80">
        <TriangleAlert
          className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning"
          aria-hidden
        />
        Copy this token now — it is shown once and cannot be recovered. Send it
        as <span className="font-mono">Authorization: Bearer …</span> on every
        request.
      </p>
      <div className="flex items-center gap-2 rounded-lg border border-neutral bg-base-100 p-2">
        <code
          className="min-w-0 flex-1 truncate font-mono text-xs"
          data-testid="api-token-secret"
        >
          {created.token}
        </code>
        <CopyButton value={created.token} label="Copy" ariaLabel="Copy API token" />
      </div>
      <div>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={onDone}
          data-testid="api-token-secret-done"
        >
          Done
        </Button>
      </div>
    </div>
  );
}
