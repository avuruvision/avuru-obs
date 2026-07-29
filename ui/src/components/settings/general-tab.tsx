"use client";

import { useState } from "react";
import { Info, Plus, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { ApiError } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import {
  useProjects,
  useCreateProject,
  useRenameProject,
  useDeleteProject,
} from "@/hooks/use-projects";
import { useAuth } from "@/hooks/use-auth";
import { useSystemStatus } from "@/hooks/use-system-status";

// Coroot-style General settings: the active project (create/rename/delete for
// UI-managed projects; read-only banner for deployment-owned ones) and this
// install's retention.
export function GeneralTab() {
  const { project, setProject } = useProject();
  const { data: projects } = useProjects();
  const { isAdmin } = useAuth();
  const { data: status } = useSystemStatus();

  const active = projects?.projects.find((p) => p.id === project);
  const editable = !!active?.editable;
  const sourceLabel =
    active?.source === "data"
      ? "discovered from data"
      : active?.source === "config"
        ? "config-defined"
        : active?.source === "db"
          ? "project"
          : "built-in";

  const rename = useRenameProject();
  const remove = useDeleteProject();
  const [labelDraft, setLabelDraft] = useState(active?.label ?? "");
  const [creating, setCreating] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Seed the label field once the project (or its stored label) resolves —
  // useState only runs its initializer on mount, so without this the field
  // would render empty for a project that already has a label. Adjust state
  // during render (React's "storing info from previous renders" pattern) keyed
  // on project+label, so it reseeds on switch and when the list loads, but not
  // while the user is typing (the stored label is stable then).
  const seedKey = `${project}:${active?.label ?? ""}`;
  const [seededKey, setSeededKey] = useState(seedKey);
  if (seededKey !== seedKey) {
    setSeededKey(seedKey);
    setLabelDraft(active?.label ?? "");
  }

  const inputClass =
    "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

  return (
    <div className="flex flex-col gap-4">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Project</CardTitle>
          <div className="flex items-center gap-2">
            {status && <span className="text-xs text-base-content/50">hub {status.version}</span>}
            {isAdmin && !creating && (
              <Button variant="secondary" size="sm" onClick={() => { setCreating(true); setErr(null); }}>
                <Plus className="h-3.5 w-3.5" /> New project
              </Button>
            )}
          </div>
        </CardHeader>
        <div className="flex flex-col gap-3 border-t border-neutral p-4">
          <div className="flex items-center gap-2">
            <span className="font-mono text-sm font-semibold">{active?.label || project}</span>
            <Badge tone="neutral">{sourceLabel}</Badge>
          </div>

          {editable && isAdmin ? (
            <label className="flex flex-col gap-1 text-xs text-base-content/60">
              Display name
              <div className="flex items-center gap-2">
                <input
                  className={inputClass}
                  value={labelDraft}
                  onChange={(e) => setLabelDraft(e.target.value)}
                  placeholder={project}
                />
                <Button
                  variant="primary"
                  size="sm"
                  disabled={rename.isPending}
                  onClick={async () => {
                    setErr(null);
                    try {
                      await rename.mutateAsync({ id: project, label: labelDraft });
                    } catch (e) {
                      setErr(e instanceof ApiError ? e.message : "request failed");
                    }
                  }}
                >
                  Save
                </Button>
              </div>
            </label>
          ) : (
            <p className="flex items-start gap-2 rounded-lg border border-info/40 bg-info/10 p-3 text-xs text-base-content/80">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-info" aria-hidden />
              This project is defined through deployment configuration and cannot
              be modified here. Declare projects with the chart&apos;s{" "}
              <code className="font-mono">projects</code> value, or create one from
              this page.
            </p>
          )}

          {err && <p className="text-xs text-error">{err}</p>}
        </div>

        {editable && isAdmin && (
          <div className="flex items-center justify-between gap-2 border-t border-neutral bg-error/5 px-4 py-3">
            <span className="text-xs text-base-content/60">
              Delete this project. Telemetry ages out by retention; a still-active
              tenant re-appears automatically.
            </span>
            <Button
              variant="ghost"
              size="sm"
              disabled={remove.isPending}
              onClick={async () => {
                setErr(null);
                try {
                  await remove.mutateAsync(project);
                  setProject("default");
                } catch (e) {
                  setErr(e instanceof ApiError ? e.message : "request failed");
                }
              }}
            >
              <Trash2 className="h-3.5 w-3.5" /> Delete
            </Button>
          </div>
        )}

        {creating && (
          <CreateProjectForm
            onDone={(newId) => {
              setCreating(false);
              if (newId) setProject(newId);
            }}
          />
        )}
      </Card>

      {status && (
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>Retention</CardTitle>
            <span className="text-xs text-base-content/50">per-signal TTL, instance-wide</span>
          </CardHeader>
          <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-4">
            {status.signals.map((s) => (
              <div key={s.signal} className="bg-base-200 p-3">
                <p className="text-xs uppercase tracking-wider text-base-content/50">{s.signal}</p>
                <p className="text-sm font-semibold">{s.retentionDays} days</p>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  );
}

// CreateProjectForm collects an id (immutable slug) + label and creates the
// project, then selects it.
function CreateProjectForm({ onDone }: { onDone: (newId?: string) => void }) {
  const create = useCreateProject();
  const [id, setId] = useState("");
  const [label, setLabel] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const inputClass =
    "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

  return (
    <form
      className="flex flex-col gap-2 border-t border-neutral px-4 py-3"
      onSubmit={async (e) => {
        e.preventDefault();
        setErr(null);
        try {
          await create.mutateAsync({ id, label });
          onDone(id);
        } catch (e2) {
          setErr(e2 instanceof ApiError ? e2.message : "request failed");
        }
      }}
    >
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Project id (immutable)
          <input
            className={inputClass}
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="staging"
            required
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Display name
          <input
            className={inputClass}
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="Staging (EU)"
          />
        </label>
      </div>
      {err && <p className="text-xs text-error">{err}</p>}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={create.isPending}>
          Create
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => onDone()}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
