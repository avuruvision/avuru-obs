"use client";

import { useState } from "react";
import { Info, Layers } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useAuth } from "@/hooks/use-auth";
import { useProject } from "@/lib/project-context";
import {
  useProjects,
  useCreateProject,
  useUpdateProject,
  useDeleteProject,
} from "@/hooks/use-projects";
import { ApiError } from "@/lib/api";
import type { Project } from "@/lib/api-types";

const inputClass =
  "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

function errMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

function sourceBadge(source: Project["source"]) {
  switch (source) {
    case "config":
      return <Badge tone="neutral">config-defined</Badge>;
    case "data":
      return <Badge tone="neutral">discovered from data</Badge>;
    case "db":
      return <Badge tone="primary">UI-managed</Badge>;
    default:
      return <Badge tone="neutral">built-in</Badge>;
  }
}

// ProjectSettingsCard renders the active project (editable for db projects,
// read-only for default/config), an admin-only "New project" form, and a delete
// affordance for db projects. Admin gate mirrors the hub's securedAdmin (403);
// this is defense in depth on the UI side.
export function ProjectSettingsCard() {
  const { isAdmin } = useAuth();
  const { project } = useProject();
  const { data } = useProjects();
  const current: Project =
    data?.projects.find((p) => p.id === project) ?? { id: project, source: "default" };
  const editable = !!current.editable && isAdmin;

  return (
    <div className="flex flex-col gap-4">
      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Project</CardTitle>
          <span className="flex items-center gap-2">
            {isAggregate(current) && <Badge tone="primary">aggregate</Badge>}
            {sourceBadge(current.source)}
          </span>
        </CardHeader>
        <div className="flex flex-col gap-3 border-t border-neutral p-4">
          <div className="text-sm">
            Project id: <span className="font-mono font-semibold">{current.id}</span>{" "}
            <span className="text-base-content/50">(immutable)</span>
          </div>
          {editable ? (
            <ProjectLabelForm key={current.id} project={current} />
          ) : (
            <p className="flex items-start gap-2 rounded-lg border border-info/40 bg-info/10 p-3 text-xs text-base-content/80">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-info" aria-hidden />
              Projects are defined through deployment configuration and cannot be
              modified here. Declare them with the chart&apos;s{" "}
              <code className="font-mono">projects</code> value and tag each
              environment&apos;s telemetry with{" "}
              <code className="font-mono">gateway.tenant</code>; any tenant seen in
              the data appears automatically.
            </p>
          )}
        </div>
      </Card>

      {editable && (
        <ProjectMembersCard
          key={current.id}
          project={current}
          all={data?.projects ?? []}
        />
      )}

      {isAdmin && <NewProjectCard />}

      {editable && <DangerZone project={current} />}
    </div>
  );
}

function ProjectLabelForm({ project }: { project: Project }) {
  const update = useUpdateProject();
  const [label, setLabel] = useState(project.label ?? "");
  const [error, setError] = useState<string | null>(null);

  async function save() {
    setError(null);
    try {
      await update.mutateAsync({ id: project.id, input: { label: label.trim() } });
    } catch (e) {
      setError(errMessage(e, "Failed to rename project"));
    }
  }

  return (
    <div className="flex flex-col gap-1">
      <label className="flex flex-col gap-1 text-sm">
        Display name
        <div className="flex gap-2">
          <input
            className={inputClass}
            value={label}
            maxLength={200}
            onChange={(e) => setLabel(e.target.value)}
          />
          <Button type="button" variant="primary" size="sm" onClick={save} disabled={update.isPending}>
            Save
          </Button>
        </div>
      </label>
      {error && <p className="text-xs text-error">{error}</p>}
    </div>
  );
}

// A project is an aggregate as soon as it has members — the hub derives it the
// same way (resolveTenants), so there is no separate flag to drift.
function isAggregate(p: Project): boolean {
  return (p.members?.length ?? 0) > 0;
}

// ProjectMembersCard turns a db project into an aggregate: it reads the union
// of its members' telemetry, filtered to what the viewer may see. Candidates
// are every OTHER project that is not itself an aggregate — membership is one
// level deep, and the hub refuses deeper with a 409.
function ProjectMembersCard({ project, all }: { project: Project; all: Project[] }) {
  const update = useUpdateProject();
  const [members, setMembers] = useState<string[]>(project.members ?? []);
  const [extra, setExtra] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const candidates = all.filter((p) => p.id !== project.id && !isAggregate(p));
  // Ids already chosen that no longer appear in the list (a cluster that has
  // not shipped data yet) must stay visible, or saving would silently drop them.
  const unlisted = members.filter((m) => !candidates.some((c) => c.id === m));

  function toggle(id: string, on: boolean) {
    setSaved(false);
    setMembers((cur) => (on ? [...cur, id] : cur.filter((m) => m !== id)));
  }

  function addUnlisted() {
    const id = extra.trim();
    if (!id || members.includes(id)) return;
    setMembers((cur) => [...cur, id]);
    setExtra("");
    setSaved(false);
  }

  async function save() {
    setError(null);
    try {
      await update.mutateAsync({ id: project.id, input: { members } });
      setSaved(true);
    } catch (e) {
      setError(errMessage(e, "Failed to save members"));
    }
  }

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Member projects</CardTitle>
        {members.length > 0 && <Badge tone="primary">{members.length} selected</Badge>}
      </CardHeader>
      <div className="flex flex-col gap-3 border-t border-neutral p-4">
        <p className="flex items-start gap-2 text-xs text-base-content/70">
          <Layers className="mt-0.5 h-3.5 w-3.5 shrink-0 text-base-content/50" aria-hidden />
          With members selected, this project shows the union of their telemetry —
          one screen across clusters. It stores nothing of its own: error triage
          and ingest keys stay on the member projects, and each viewer sees only
          the members they have access to. Clearing every member turns it back
          into an ordinary project.
        </p>
        {candidates.length === 0 && unlisted.length === 0 ? (
          <p className="text-xs text-base-content/50">
            No other projects yet. Create one, or add an id below before its
            cluster reports.
          </p>
        ) : (
          <ul className="flex flex-col gap-1">
            {candidates.map((p) => (
              <li key={p.id} className="flex items-center justify-between gap-3 text-sm">
                <span className="truncate">
                  {p.label || p.id}{" "}
                  {p.label && <span className="font-mono text-xs text-base-content/50">{p.id}</span>}
                </span>
                <input
                  type="checkbox"
                  className="toggle toggle-primary toggle-sm"
                  aria-label={`Include ${p.id}`}
                  checked={members.includes(p.id)}
                  onChange={(e) => toggle(p.id, e.target.checked)}
                />
              </li>
            ))}
            {unlisted.map((id) => (
              <li key={id} className="flex items-center justify-between gap-3 text-sm">
                <span className="truncate font-mono text-xs">
                  {id} <span className="text-base-content/50">(no data yet)</span>
                </span>
                <input
                  type="checkbox"
                  className="toggle toggle-primary toggle-sm"
                  aria-label={`Include ${id}`}
                  checked
                  onChange={(e) => toggle(id, e.target.checked)}
                />
              </li>
            ))}
          </ul>
        )}
        <label className="flex flex-col gap-1 text-sm">
          Add a project id
          <div className="flex gap-2">
            <input
              className={inputClass}
              value={extra}
              placeholder="prod-eu"
              onChange={(e) => setExtra(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addUnlisted();
                }
              }}
            />
            <Button type="button" variant="secondary" size="sm" onClick={addUnlisted}>
              Add
            </Button>
          </div>
          <span className="text-xs text-base-content/50">
            For a cluster that has not reported yet — it appears here once it does.
          </span>
        </label>
        {error && <p className="text-xs text-error">{error}</p>}
        <div className="flex items-center gap-2">
          <Button type="button" variant="primary" size="sm" onClick={save} disabled={update.isPending}>
            Save members
          </Button>
          {saved && !error && (
            <span className="text-xs text-base-content/60">
              Saved. Other replicas follow within 30 seconds.
            </span>
          )}
        </div>
      </div>
    </Card>
  );
}

function NewProjectCard() {
  const create = useCreateProject();
  const { setProject } = useProject();
  const [open, setOpen] = useState(false);
  const [id, setId] = useState("");
  const [label, setLabel] = useState("");
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setError(null);
    try {
      await create.mutateAsync({ id: id.trim(), label: label.trim() });
      setProject(id.trim());
      setOpen(false);
      setId("");
      setLabel("");
    } catch (e) {
      setError(errMessage(e, "Failed to create project"));
    }
  }

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>New project</CardTitle>
        {!open && (
          <Button type="button" variant="secondary" size="sm" onClick={() => setOpen(true)}>
            New project
          </Button>
        )}
      </CardHeader>
      {open && (
        <div className="flex flex-col gap-3 border-t border-neutral p-4">
          <label className="flex flex-col gap-1 text-sm">
            Project id
            <input
              className={inputClass}
              value={id}
              onChange={(e) => setId(e.target.value)}
              placeholder="team-a"
            />
            <span className="text-xs text-base-content/50">
              Lowercase letters, digits, and hyphens; starts with a letter. Immutable.
            </span>
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Project name
            <input
              className={inputClass}
              value={label}
              maxLength={200}
              onChange={(e) => setLabel(e.target.value)}
            />
          </label>
          {error && <p className="text-xs text-error">{error}</p>}
          <div className="flex gap-2">
            <Button type="button" variant="primary" size="sm" onClick={submit} disabled={create.isPending}>
              New project
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
}

function DangerZone({ project }: { project: Project }) {
  const del = useDeleteProject();
  const { setProject } = useProject();
  const [error, setError] = useState<string | null>(null);

  async function remove() {
    setError(null);
    try {
      await del.mutateAsync(project.id);
      setProject("default");
    } catch (e) {
      setError(errMessage(e, "Failed to delete project"));
    }
  }

  return (
    <Card className="overflow-hidden border-error/40">
      <CardHeader>
        <CardTitle>Danger zone</CardTitle>
      </CardHeader>
      <div className="flex flex-col gap-2 border-t border-neutral p-4">
        <div className="flex items-center justify-between gap-4">
          <p className="text-sm text-base-content/70">
            Delete this project. Its telemetry ages out by retention; the id can be
            re-created later.
          </p>
          <Button type="button" variant="danger" size="sm" onClick={remove} disabled={del.isPending}>
            Delete project
          </Button>
        </div>
        {error && <p className="text-xs text-error">{error}</p>}
      </div>
    </Card>
  );
}
