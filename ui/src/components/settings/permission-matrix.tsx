"use client";

import { useMemo, useState } from "react";
import { Check, Minus, Search } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import type { PermissionArea } from "@/lib/api-types";

// Roles in order of privilege — the matrix reads left to right as "and also".
const COLUMNS = ["viewer", "editor", "admin"] as const;
type RoleName = (typeof COLUMNS)[number];

const RANK: Record<string, number> = { viewer: 1, editor: 2, admin: 3 };

// What a role can do in one area, as three states rather than a checkbox grid:
// "Read & change" is the thing worth spotting, so it is the only coloured one.
type Access = "write" | "read" | "none";

function accessOf(role: RoleName, area: PermissionArea): Access {
  const can = (need?: string) => !!need && RANK[role] >= (RANK[need] ?? 99);
  if (can(area.write)) return "write";
  if (can(area.read)) return "read";
  return "none";
}

// Areas arrive from the hub already sorted by group, then label. Folding them
// here preserves that order instead of imposing a second one that could drift.
function byGroup(areas: PermissionArea[]): { group: string; areas: PermissionArea[] }[] {
  const out: { group: string; areas: PermissionArea[] }[] = [];
  for (const a of areas) {
    const group = a.group || "Other";
    const last = out[out.length - 1];
    if (last && last.group === group) last.areas.push(a);
    else out.push({ group, areas: [a] });
  }
  return out;
}

// Who can do what. Every cell comes from the hub, which derives it from the
// authorization its routes actually registered with — a hand-written table
// here would be a second copy of the rules and would be wrong the first time
// one of them changed, in the direction that matters least until it matters
// most.
//
// Twenty-six areas is more than anyone reads top to bottom, so the table is
// sectioned the way the sidebar is and takes a filter: the question people
// actually arrive with is "what can an editor do with alerts", not "list every
// permission".
export function PermissionMatrix({ areas }: { areas: PermissionArea[] }) {
  const [query, setQuery] = useState("");

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const matching = q
      ? areas.filter(
          (a) => a.label.toLowerCase().includes(q) || a.area.toLowerCase().includes(q),
        )
      : areas;
    return byGroup(matching);
  }, [areas, query]);

  const shown = groups.reduce((n, g) => n + g.areas.length, 0);

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>What each role can do</CardTitle>
        <div className="flex items-center gap-3">
          <span className="hidden text-xs text-base-content/45 sm:inline">
            {query ? `${shown} of ${areas.length}` : `${areas.length} areas`}
          </span>
          <div className="relative">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-base-content/40"
              aria-hidden
            />
            <input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter areas…"
              aria-label="Filter permission areas"
              data-testid="permission-matrix-filter"
              className="h-8 w-40 rounded-lg border border-neutral bg-base-100 pl-8 pr-2.5 text-sm outline-none placeholder:text-base-content/40 focus:border-primary sm:w-56"
            />
          </div>
        </div>
      </CardHeader>

      <div className="max-h-[60vh] overflow-auto">
        <table className="table-dense w-full text-sm" data-testid="permission-matrix">
          <thead className="sticky top-0 z-10 bg-base-200">
            <tr className="border-y border-neutral text-left">
              <th className="w-1/2">Area</th>
              {COLUMNS.map((role) => (
                <th key={role} className="text-center capitalize">
                  {role}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {groups.length === 0 && (
              <tr>
                <td colSpan={4} className="py-6 text-center text-sm text-base-content/45">
                  No area matches “{query}”.
                </td>
              </tr>
            )}
            {groups.map((g) => (
              <GroupSection key={g.group} group={g.group} areas={g.areas} />
            ))}
          </tbody>
        </table>
      </div>

      <div className="flex flex-col gap-2 border-t border-neutral px-4 py-3">
        <Legend />
        <p className="text-xs text-base-content/45">
          Read and write are granted <strong>per project</strong> — a viewer on
          one project sees nothing of another. Administration is global: it is
          not something an admin grant on a single project confers. Assign roles
          in <span className="font-mono">Settings → Users</span>, or map them
          from your identity provider’s groups.
        </p>
      </div>
    </Card>
  );
}

function GroupSection({ group, areas }: { group: string; areas: PermissionArea[] }) {
  return (
    <>
      <tr className="bg-base-300/40">
        <th
          colSpan={4}
          className="px-4 py-1.5 text-left text-xs font-semibold uppercase tracking-wide text-base-content/50"
          scope="colgroup"
        >
          {group}
        </th>
      </tr>
      {areas.map((a) => (
        <AreaRow key={a.area} a={a} />
      ))}
    </>
  );
}

function AreaRow({ a }: { a: PermissionArea }) {
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td>
        <span className="font-medium">{a.label}</span>{" "}
        <span className="font-mono text-xs text-base-content/35">/{a.area}</span>
      </td>
      {COLUMNS.map((role) => (
        <td key={role} className="text-center" data-testid={`cell-${a.area}-${role}`}>
          <Cell access={accessOf(role, a)} />
        </td>
      ))}
    </tr>
  );
}

// A cell says the strongest thing the role can do in that area. "Read" without
// a write marker is not an omission: most areas have no write route at all,
// and showing a dash for "nobody can" would read the same as "you can't".
function Cell({ access }: { access: Access }) {
  if (access === "write") {
    return (
      <span className="inline-flex items-center gap-1 text-success">
        <Check className="h-3.5 w-3.5" /> Read &amp; change
      </span>
    );
  }
  if (access === "read") {
    return (
      <span className="inline-flex items-center gap-1 text-base-content/70">
        <Check className="h-3.5 w-3.5" /> Read
      </span>
    );
  }
  return <Minus className="mx-auto h-3.5 w-3.5 text-base-content/25" aria-label="no access" />;
}

function Legend() {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-base-content/60">
      <span className="inline-flex items-center gap-1 text-success">
        <Check className="h-3.5 w-3.5" /> Read &amp; change
      </span>
      <span className="inline-flex items-center gap-1">
        <Check className="h-3.5 w-3.5" /> Read only
      </span>
      <span className="inline-flex items-center gap-1">
        <Minus className="h-3.5 w-3.5 text-base-content/25" /> No access
      </span>
    </div>
  );
}
