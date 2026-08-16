"use client";

import { useState } from "react";
import { Plus, Pencil, Trash2 } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { ApiError } from "@/lib/api";
import { useAuth } from "@/hooks/use-auth";
import {
  useOIDCMapping,
  useSaveOIDCMapping,
  useDeleteOIDCMapping,
  useResetOIDCMapping,
} from "@/hooks/use-oidc-mapping";
import { OIDCMappingForm } from "./oidc-mapping-form";
import type { OIDCMappingInput, OIDCMappingRule } from "@/lib/api-types";

function errText(err: unknown): string {
  return err instanceof ApiError ? err.message : "request failed";
}

function describeProjects(projects?: string[]): string {
  if (!projects || projects.length === 0) return "no projects — grants nothing";
  return `projects: ${projects.join(", ")}`;
}

// OIDC group→role mapping: which identity-provider groups grant which role,
// on which projects, to anyone who signs in through SSO. Same shape as
// ServiceGroupsPanel — rules declared in the chart's `auth.oidc.mapping`
// values render read-only beside the ones authored here — with one addition
// that panel doesn't need: an authored rule whose group the chart ALSO
// declares is stored and stays editable (not refused), because the config
// deliberately wins that collision rather than the write being rejected
// (auth.MergeMapping) — so it is marked shadowed instead, with its own
// explanation, and it is still deletable (d7ef9a0).
//
// Only mounted (access-tab.tsx) when SSO is configured — the hub 404s these
// routes otherwise.
export function OIDCMappingPanel() {
  const { isAdmin } = useAuth();
  const { data, isLoading, isError } = useOIDCMapping(true);
  const save = useSaveOIDCMapping();
  const remove = useDeleteOIDCMapping();
  const reset = useResetOIDCMapping();

  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [confirmReset, setConfirmReset] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  if (isLoading) return <CenteredSpinner />;
  if (isError || !data) {
    return (
      <Card className="p-8 text-center text-sm text-error">
        Couldn’t reach the hub to read the OIDC group mapping.
      </Card>
    );
  }

  const rules = data.rules;
  const busy = save.isPending || remove.isPending || reset.isPending;

  const doDelete = async (group: string) => {
    setActionError(null);
    try {
      await remove.mutateAsync(group);
    } catch (err) {
      setActionError(errText(err));
    }
  };

  const doReset = () => {
    if (!confirmReset) {
      setConfirmReset(true);
      return;
    }
    setConfirmReset(false);
    setActionError(null);
    reset.mutate(undefined, { onError: (err) => setActionError(errText(err)) });
  };

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>OIDC group mapping</CardTitle>
        {isAdmin && !adding && (
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setAdding(true);
              setEditing(null);
              setConfirmReset(false);
            }}
            data-testid="add-oidc-mapping"
          >
            <Plus className="h-3.5 w-3.5" /> Add rule
          </Button>
        )}
      </CardHeader>
      <p className="px-4 pb-2 text-xs text-base-content/55">
        Maps an identity-provider group to a role on one or more projects, on
        top of the chart&apos;s <span className="font-mono">auth.oidc.mapping</span> values.
        A change here applies to that group&apos;s next sign-in or token
        refresh, and reaches every hub replica within about 15 seconds — not
        instantly, so don&apos;t read a stale result on another replica as a
        failed save.
      </p>

      {rules.length === 0 && !adding ? (
        <p className="px-4 pb-4 text-sm text-base-content/55" data-testid="oidc-mapping-empty">
          No group rules yet. Everyone signing in through SSO falls back to
          the chart&apos;s default role until you add one.
        </p>
      ) : (
        <ul className="divide-y divide-neutral" data-testid="oidc-mapping-list">
          {rules.map((r) => (
            <OIDCMappingRow
              key={`${r.source}:${r.group}`}
              rule={r}
              isAdmin={isAdmin}
              busy={busy}
              editing={editing === r.group}
              onEdit={() => {
                setEditing(r.group);
                setAdding(false);
              }}
              onCancelEdit={() => setEditing(null)}
              onDelete={() => void doDelete(r.group)}
              onSave={async (input) => {
                await save.mutateAsync(input);
                setEditing(null);
              }}
            />
          ))}
        </ul>
      )}

      {adding && (
        <OIDCMappingForm
          busy={busy}
          onCancel={() => setAdding(false)}
          onSubmit={async (input) => {
            await save.mutateAsync(input);
            setAdding(false);
          }}
        />
      )}

      {isAdmin && (
        <div className="flex flex-col gap-1.5 border-t border-neutral p-4">
          <div className="flex items-center gap-2">
            <Button
              variant={confirmReset ? "danger" : "ghost"}
              size="sm"
              disabled={busy}
              onClick={doReset}
              data-testid="reset-oidc-mapping"
            >
              {confirmReset ? "Confirm reset" : "Reset to chart defaults"}
            </Button>
            <span className="text-xs text-base-content/45">
              deletes every rule authored here; the chart&apos;s own rules are
              unaffected
            </span>
          </div>
          {actionError && (
            <p className="text-xs text-error" data-testid="oidc-mapping-error">
              {actionError}
            </p>
          )}
        </div>
      )}
    </Card>
  );
}

function OIDCMappingRow({
  rule: r,
  isAdmin,
  busy,
  editing,
  onEdit,
  onCancelEdit,
  onDelete,
  onSave,
}: {
  rule: OIDCMappingRule;
  isAdmin: boolean;
  busy: boolean;
  editing: boolean;
  onEdit: () => void;
  onCancelEdit: () => void;
  onDelete: () => void;
  onSave: (input: { group: string } & OIDCMappingInput) => Promise<unknown>;
}) {
  return (
    <li className="flex flex-col px-4 py-2.5 text-sm">
      <div className="flex items-center gap-2">
        <span className="font-mono font-medium">{r.group}</span>
        {r.invalid ? (
          <Badge tone="error">invalid role</Badge>
        ) : (
          <Badge tone={r.role === "admin" ? "error" : "neutral"}>{r.role}</Badge>
        )}
        <Badge tone={r.editable ? "primary" : "neutral"}>
          {r.source === "db" ? "ui" : "config"}
        </Badge>
        {r.shadowed && (
          <Badge
            tone="warning"
            title="The chart config now declares this group too, and it wins — this rule grants nothing"
          >
            overridden by config
          </Badge>
        )}
        {isAdmin && r.editable && (
          <span className="ml-auto flex items-center gap-1">
            <Button
              variant="ghost"
              size="sm"
              onClick={onEdit}
              data-testid={`edit-oidc-mapping-${r.group}`}
            >
              <Pencil className="h-3.5 w-3.5" /> Edit
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={onDelete}
              disabled={busy}
              data-testid={`delete-oidc-mapping-${r.group}`}
            >
              <Trash2 className="h-3.5 w-3.5" /> Delete
            </Button>
          </span>
        )}
      </div>

      {r.invalid ? (
        <span className="truncate text-xs text-base-content/55">
          saved with role “{r.invalidRole}”
        </span>
      ) : (
        <span className="truncate text-xs text-base-content/55">
          {describeProjects(r.projects)}
        </span>
      )}

      {r.shadowed && (
        <span className="text-xs text-warning">
          the chart declares “{r.group}” too, and config wins — this rule
          stopped applying
        </span>
      )}
      {r.invalid && (
        <span className="text-xs text-error">
          this build doesn’t recognize that role — grants nothing until fixed
          or deleted
        </span>
      )}
      {!r.editable && (
        <span className="text-xs text-base-content/40">
          declared in the chart&apos;s{" "}
          <span className="font-mono">auth.oidc.mapping</span> values
        </span>
      )}

      {editing && r.editable && (
        <OIDCMappingForm initial={r} busy={busy} onCancel={onCancelEdit} onSubmit={onSave} />
      )}
    </li>
  );
}
