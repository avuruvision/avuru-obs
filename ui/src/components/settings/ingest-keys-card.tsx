"use client";

import { useState } from "react";
import { KeyRound, TriangleAlert } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { useAuth } from "@/hooks/use-auth";
import { useProject } from "@/lib/project-context";
import {
  useIngestKeys,
  useCreateIngestKey,
  useRevokeIngestKey,
} from "@/hooks/use-ingest-keys";
import { ApiError } from "@/lib/api";
import type { CreateIngestKeyResponse, IngestKey } from "@/lib/api-types";

const inputClass =
  "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

function fmtDate(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString();
}

// IngestKeysCard manages a project's ingest API keys: list (prefix + metadata),
// create (raw secret shown once), and revoke. Admin-only — mirrors the hub's
// securedAdmin (403); this UI gate is defense in depth. Keys authenticate a
// project's telemetry at the gateway and, in enforce mode, stamp the project as
// the authoritative tenant.
export function IngestKeysCard() {
  const { isAdmin } = useAuth();
  const { project } = useProject();
  const { data, isLoading } = useIngestKeys(project, isAdmin);

  if (!isAdmin) return null;

  const keys = data?.keys ?? [];

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>
          <span className="inline-flex items-center gap-2">
            <KeyRound className="h-4 w-4 text-base-content/60" aria-hidden />
            Ingest API keys
          </span>
        </CardTitle>
        <span className="text-xs text-base-content/50">
          authenticate <span className="font-mono">{project}</span> telemetry
        </span>
      </CardHeader>

      <div className="flex flex-col gap-3 border-t border-neutral p-4">
        <p className="text-xs text-base-content/60">
          A key authenticates OTLP ingest for this project. In{" "}
          <span className="font-mono">enforce</span> mode the key&apos;s project
          becomes the authoritative tenant, overriding any client-supplied value.
        </p>

        {isLoading ? (
          <p className="text-xs text-base-content/45">Loading keys…</p>
        ) : keys.length === 0 ? (
          <p className="text-xs text-base-content/45">
            No keys yet. Create one below to start authenticating ingest.
          </p>
        ) : (
          <ul className="flex flex-col divide-y divide-neutral rounded-lg border border-neutral">
            {keys.map((k) => (
              <KeyRow key={k.keyHash} k={k} project={project} />
            ))}
          </ul>
        )}

        <CreateKeyForm project={project} />
      </div>
    </Card>
  );
}

function KeyRow({ k, project }: { k: IngestKey; project: string }) {
  const revoke = useRevokeIngestKey(project);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function remove() {
    setError(null);
    try {
      await revoke.mutateAsync(k.keyHash);
    } catch (e) {
      setError(errMessage(e, "Failed to revoke key"));
      setConfirming(false);
    }
  }

  return (
    <li className="flex flex-wrap items-center justify-between gap-2 p-3">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="truncate text-sm font-medium">{k.name}</span>
        <span className="text-xs text-base-content/50">
          <span className="font-mono">{k.prefix}…</span> · created{" "}
          {fmtDate(k.createdAt)}
          {k.createdBy ? ` by ${k.createdBy}` : ""}
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
        >
          Revoke
        </Button>
      )}
    </li>
  );
}

function CreateKeyForm({ project }: { project: string }) {
  const create = useCreateIngestKey(project);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<CreateIngestKeyResponse | null>(null);

  async function submit() {
    setError(null);
    try {
      const res = await create.mutateAsync({ name: name.trim() });
      setRevealed(res);
      setName("");
    } catch (e) {
      setError(errMessage(e, "Failed to create key"));
    }
  }

  if (revealed) {
    return <RevealPanel created={revealed} onDone={() => setRevealed(null)} />;
  }

  return (
    <div className="flex flex-col gap-2 border-t border-neutral pt-3">
      <label className="flex flex-col gap-1 text-sm">
        New key name
        <div className="flex gap-2">
          <input
            className={inputClass}
            value={name}
            maxLength={200}
            placeholder="prod-exporter"
            onChange={(e) => setName(e.target.value)}
          />
          <Button
            type="button"
            variant="primary"
            size="sm"
            onClick={submit}
            disabled={create.isPending || name.trim() === ""}
          >
            Create key
          </Button>
        </div>
      </label>
      {error && <p className="text-xs text-error">{error}</p>}
    </div>
  );
}

// RevealPanel shows the raw key ONCE. There is no way to see it again — the hub
// stores only its hash — so the copy affordance and the warning are prominent.
function RevealPanel({
  created,
  onDone,
}: {
  created: CreateIngestKeyResponse;
  onDone: () => void;
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-warning/50 bg-warning/10 p-3">
      <p className="flex items-start gap-2 text-xs text-base-content/80">
        <TriangleAlert
          className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning"
          aria-hidden
        />
        Copy this key now — it is shown once and cannot be recovered. Store it in
        your exporter&apos;s <span className="font-mono">Authorization</span>{" "}
        header.
      </p>
      <div className="flex items-center gap-2 rounded-lg border border-neutral bg-base-100 p-2">
        <code className="min-w-0 flex-1 truncate font-mono text-xs" data-testid="ingest-key-secret">
          {created.key}
        </code>
        <CopyButton value={created.key} label="Copy" ariaLabel="Copy ingest key" />
      </div>
      <div>
        <Button type="button" variant="secondary" size="sm" onClick={onDone}>
          Done
        </Button>
      </div>
    </div>
  );
}
