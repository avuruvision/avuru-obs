"use client";

import { useState } from "react";
import { Plus, Pencil, Trash2 } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { useAuth } from "@/hooks/use-auth";
import {
  useServiceGroups,
  useCreateServiceGroup,
  useUpdateServiceGroup,
  useDeleteServiceGroup,
} from "@/hooks/use-service-groups";
import { ServiceGroupForm } from "./service-group-form";

// Service health groups: which services belong together and how critical they
// are. Groups declared in the chart's `serviceGroups` values stay read-only
// here — the hub lets the config win a name collision, so offering an edit
// would promise a change that never takes effect. Everything else is authored
// here and applies to the next health read; no redeploy.
export function ServiceGroupsPanel() {
  const { isAdmin } = useAuth();
  const { data, isLoading } = useServiceGroups();
  const create = useCreateServiceGroup();
  const update = useUpdateServiceGroup();
  const remove = useDeleteServiceGroup();

  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);

  if (isLoading) return <CenteredSpinner />;
  const groups = data?.groups ?? [];
  const busy = create.isPending || update.isPending || remove.isPending;

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Service groups</CardTitle>
        {isAdmin && !adding && (
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setAdding(true);
              setEditing(null);
            }}
            data-testid="add-service-group"
          >
            <Plus className="h-3.5 w-3.5" /> Add group
          </Button>
        )}
      </CardHeader>
      <p className="px-4 pb-2 text-xs text-base-content/55">
        Services matched by no group still appear, grouped by their namespace at
        the default tier — so nothing disappears while you organize.
      </p>
      {groups.length === 0 && !adding ? (
        <p className="px-4 pb-4 text-sm text-base-content/55" data-testid="service-groups-empty">
          No groups yet. Add one to say which services matter — a group is a
          name, a criticality tier, and the namespaces or services it covers.
        </p>
      ) : (
        <ul className="divide-y divide-neutral" data-testid="service-groups-list">
          {groups.map((g) => (
            <li key={`${g.source}:${g.name}`} className="flex flex-col px-4 py-2.5 text-sm">
              <div className="flex items-center gap-2">
                <span className="font-mono font-medium">{g.name}</span>
                <Badge tone={g.tier === "T0" ? "error" : "neutral"}>{g.tier}</Badge>
                <Badge tone={g.editable ? "primary" : "neutral"}>
                  {g.source === "db" ? "ui" : "config"}
                </Badge>
                {g.shadowed && (
                  <Badge
                    tone="warning"
                    title="The chart config now declares this name, and it wins — this group no longer groups anything"
                  >
                    overridden by config
                  </Badge>
                )}
                {isAdmin && g.editable && (
                  <span className="ml-auto flex items-center gap-1">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setEditing(g.name);
                        setAdding(false);
                      }}
                      data-testid={`edit-service-group-${g.name}`}
                    >
                      <Pencil className="h-3.5 w-3.5" /> Edit
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => remove.mutate(g.name)}
                      disabled={busy}
                      data-testid={`delete-service-group-${g.name}`}
                    >
                      <Trash2 className="h-3.5 w-3.5" /> Delete
                    </Button>
                  </span>
                )}
              </div>
              <span className="truncate text-xs text-base-content/55">
                {describeSelector(g.namespaces, g.services)}
              </span>
              {!g.editable && (
                <span className="text-xs text-base-content/40">
                  declared in the chart&apos;s <span className="font-mono">serviceGroups</span> values
                </span>
              )}
              {editing === g.name && g.editable && (
                <ServiceGroupForm
                  initial={g}
                  busy={busy}
                  onCancel={() => setEditing(null)}
                  onSubmit={async (input) => {
                    await update.mutateAsync(input);
                    setEditing(null);
                  }}
                />
              )}
            </li>
          ))}
        </ul>
      )}
      {adding && (
        <ServiceGroupForm
          busy={busy}
          onCancel={() => setAdding(false)}
          onSubmit={async (input) => {
            await create.mutateAsync(input);
            setAdding(false);
          }}
        />
      )}
    </Card>
  );
}

function describeSelector(namespaces: string[], services: string[]): string {
  const parts: string[] = [];
  if (namespaces.length) parts.push(`namespaces: ${namespaces.join(", ")}`);
  if (services.length) parts.push(`services: ${services.join(", ")}`);
  return parts.join(" · ") || "no selector";
}
