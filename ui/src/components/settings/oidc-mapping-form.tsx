"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { ApiError } from "@/lib/api";
import type { OIDCMappingInput, OIDCMappingRule } from "@/lib/api-types";

const ROLES = [
  { value: "viewer", label: "viewer" },
  { value: "editor", label: "editor" },
  { value: "admin", label: "admin" },
];

// Projects are edited as one-per-line text, same reasoning as
// ServiceGroupForm's namespaces/services: the hub trims and de-duplicates
// what arrives, so pasting a list is fine. "*" is a real project scope here
// too, exactly as it is on a user grant — it means every project.
function toLines(v: string[]): string {
  return v.join("\n");
}

function fromLines(v: string): string[] {
  return v
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
}

// Inline create/edit form for one OIDC group→role rule — same shape as
// ServiceGroupForm: the group is the identity (disabled on edit). Editing an
// `invalid` row (a stored role the hub's ParseRole no longer accepts) starts
// the role picker at "viewer" rather than trying to preselect the offending
// string, since the picker only offers the roles that actually parse; the
// bad value the row was saved with stays visible in the list until this
// save replaces it.
export function OIDCMappingForm({
  initial,
  onSubmit,
  onCancel,
  busy,
}: {
  initial?: OIDCMappingRule;
  onSubmit: (input: { group: string } & OIDCMappingInput) => Promise<unknown>;
  onCancel: () => void;
  busy: boolean;
}) {
  const [group, setGroup] = useState(initial?.group ?? "");
  const [role, setRole] = useState(initial?.role ?? "viewer");
  const [projects, setProjects] = useState(toLines(initial?.projects ?? []));
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const input = {
      group: group.trim(),
      role,
      projects: fromLines(projects),
    };
    if (!input.projects.length) {
      setError("Add at least one project — a rule with none grants nothing.");
      return;
    }
    try {
      await onSubmit(input);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "request failed");
    }
  };

  const inputClass =
    "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";
  const areaClass =
    "min-h-16 w-full rounded-lg border border-neutral bg-base-100 px-2.5 py-1.5 font-mono text-xs focus-visible:outline-2 focus-visible:outline-primary";

  return (
    <form
      onSubmit={submit}
      className="flex flex-col gap-2 border-t border-neutral px-4 py-3"
      data-testid="oidc-mapping-form"
    >
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Identity-provider group
          <input
            className={inputClass}
            value={group}
            onChange={(e) => setGroup(e.target.value)}
            placeholder="platform-oncall"
            disabled={!!initial}
            required
            data-testid="oidc-mapping-group"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Role
          <Select
            value={role}
            options={ROLES}
            onChange={setRole}
            ariaLabel="Role"
            className="h-8"
          />
        </label>
      </div>
      <label className="flex flex-col gap-1 text-xs text-base-content/60">
        Projects <span className="text-base-content/40">(one per line — * means every project)</span>
        <textarea
          className={areaClass}
          value={projects}
          onChange={(e) => setProjects(e.target.value)}
          placeholder={"*\ndefault"}
          data-testid="oidc-mapping-projects"
        />
      </label>
      {error && (
        <p className="text-xs text-error" data-testid="oidc-mapping-form-error">
          {error}
        </p>
      )}
      <div className="flex items-center gap-2">
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={busy}
          data-testid="oidc-mapping-save"
        >
          {initial ? "Save" : "Add rule"}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
