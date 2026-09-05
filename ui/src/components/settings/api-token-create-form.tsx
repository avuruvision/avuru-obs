"use client";

import { useState } from "react";
import { TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { cn } from "@/lib/cn";
import { useCreateApiToken } from "@/hooks/use-api-tokens";
import { ApiError } from "@/lib/api";
import type { CreateApiTokenResponse } from "@/lib/api-types";

const inputClass =
  "h-9 rounded-lg border border-neutral bg-base-100 px-3 text-sm outline-none placeholder:text-base-content/40 focus:border-primary";

// Lifetimes as one-click choices rather than a dropdown: there are four of
// them, they fit on a line, and the whole point of this form is that a CI job
// is blocked until someone gets through it. "Custom" is the escape hatch — the
// hub takes any positive day count, and a dropdown of four could not say so.
// "Never" is a real choice, not a missing one: the hub reads expiresInDays
// 0/absent as no expiry.
const PRESETS = [
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "1 year" },
  { value: "0", label: "Never" },
  { value: "custom", label: "Custom…" },
] as const;

const RECOMMENDED = "90";
const MAX_DAYS = 3650;

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

function expiryDate(days: number): string {
  return new Date(Date.now() + days * 86_400_000).toLocaleDateString(undefined, {
    dateStyle: "medium",
  });
}

export function CreateTokenForm() {
  const create = useCreateApiToken();
  const [name, setName] = useState("");
  const [preset, setPreset] = useState<string>(RECOMMENDED);
  const [customDays, setCustomDays] = useState("180");
  const [error, setError] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<CreateApiTokenResponse | null>(null);

  const custom = preset === "custom";
  const days = custom ? Number(customDays) : Number(preset);
  const daysValid =
    !custom || (Number.isInteger(days) && days >= 1 && days <= MAX_DAYS);
  const canSubmit = name.trim() !== "" && daysValid && !create.isPending;

  async function submit() {
    if (!canSubmit) return;
    setError(null);
    try {
      const res = await create.mutateAsync({
        name: name.trim(),
        expiresInDays: days,
      });
      setRevealed(res);
      setName("");
      setPreset(RECOMMENDED);
    } catch (e) {
      setError(errMessage(e, "Failed to create token"));
    }
  }

  if (revealed) {
    return <RevealPanel created={revealed} onDone={() => setRevealed(null)} />;
  }

  return (
    <div className="flex flex-col gap-3 border-t border-neutral pt-3">
      <div className="flex flex-wrap items-end gap-2">
        <label className="flex flex-1 flex-col gap-1 text-sm" htmlFor="api-token-name">
          Name it after the job that will hold it
          <input
            id="api-token-name"
            className={cn(inputClass, "w-full min-w-48")}
            value={name}
            maxLength={200}
            placeholder="ci-deploy"
            autoComplete="off"
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                void submit();
              }
            }}
            data-testid="api-token-name"
          />
        </label>
        <Button
          type="button"
          variant="primary"
          onClick={submit}
          disabled={!canSubmit}
          data-testid="create-api-token"
        >
          {create.isPending ? "Creating…" : "Create token"}
        </Button>
      </div>

      <div className="flex flex-col gap-1.5">
        <span className="text-sm">Expires</span>
        <div className="flex flex-wrap gap-1.5" role="radiogroup" aria-label="Token expiry">
          {PRESETS.map((p) => (
            <button
              key={p.value}
              type="button"
              role="radio"
              aria-checked={preset === p.value}
              onClick={() => setPreset(p.value)}
              data-testid={`api-token-expiry-${p.value}`}
              className={cn(
                "h-8 rounded-lg border px-3 text-sm transition-colors",
                preset === p.value
                  ? "border-primary bg-primary/10 text-primary"
                  : "border-neutral bg-base-100 text-base-content/70 hover:border-base-content/30",
              )}
            >
              {p.label}
            </button>
          ))}
          {custom && (
            <span className="flex items-center gap-1.5">
              <input
                type="number"
                min={1}
                max={MAX_DAYS}
                value={customDays}
                aria-label="Custom expiry in days"
                onChange={(e) => setCustomDays(e.target.value)}
                data-testid="api-token-custom-days"
                className={cn(inputClass, "h-8 w-24")}
              />
              <span className="text-sm text-base-content/60">days</span>
            </span>
          )}
        </div>
        <p className="text-xs text-base-content/50" data-testid="api-token-expiry-preview">
          {!daysValid ? (
            <span className="text-error">
              Enter a whole number of days between 1 and {MAX_DAYS}.
            </span>
          ) : days > 0 ? (
            <>
              Stops working on <strong>{expiryDate(days)}</strong>. A token that
              expires is one you cannot forget to revoke.
            </>
          ) : (
            <>
              Never expires — it keeps working until someone revokes it here.
              Prefer a date unless the job has no way to rotate.
            </>
          )}
        </p>
      </div>

      {error && <p className="text-xs text-error">{error}</p>}
    </div>
  );
}

// RevealPanel shows the raw token ONCE. There is no way to see it again — the
// hub stores only its hash — so the copy affordance and the warning are
// prominent, and the ready-made request is right there: the next thing anyone
// does with a new token is check that it works.
function RevealPanel({
  created,
  onDone,
}: {
  created: CreateApiTokenResponse;
  onDone: () => void;
}) {
  const origin = typeof window === "undefined" ? "https://your-hub" : window.location.origin;
  const curl = `curl -H "Authorization: Bearer ${created.token}" \\\n  ${origin}/api/v1/services`;

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-warning/50 bg-warning/10 p-3">
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

      <div className="flex flex-col gap-1">
        <span className="flex items-center justify-between text-xs text-base-content/60">
          Try it
          <CopyButton value={curl} label="Copy" ariaLabel="Copy example request" />
        </span>
        <pre
          className="overflow-x-auto rounded-lg border border-neutral bg-base-100 p-2 font-mono text-xs text-base-content/80"
          data-testid="api-token-curl"
        >
          {curl}
        </pre>
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
