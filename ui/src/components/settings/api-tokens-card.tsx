"use client";

import { useState } from "react";
import { KeySquare } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useApiTokens, useRevokeApiToken } from "@/hooks/use-api-tokens";
import { ApiError } from "@/lib/api";
import { CreateTokenForm } from "./api-token-create-form";
import type { ApiToken } from "@/lib/api-types";

const SOON_MS = 7 * 86_400_000;

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

// A token that dies next Tuesday is a pipeline that breaks next Tuesday. The
// list says so a week ahead, while there is still time to rotate.
function expiresSoon(t: ApiToken): boolean {
  if (!t.expiresAt) return false;
  const left = new Date(t.expiresAt).getTime() - Date.now();
  return left > 0 && left <= SOON_MS;
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
          {!expired && expiresSoon(t) && (
            <Badge tone="warning" title="Rotate this one before it takes a job down with it">
              expires soon
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
