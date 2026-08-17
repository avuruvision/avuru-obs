"use client";

import { Check, Minus, ShieldAlert } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CenteredSpinner } from "@/components/ui/spinner";
import { apiGet } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import { usePermissions } from "@/hooks/use-permissions";
import { ApiTokensCard } from "./api-tokens-card";
import { OIDCMappingPanel } from "./oidc-mapping-panel";
import type { AuthConfig, PermissionArea, PermissionRole } from "@/lib/api-types";

// Roles in order of privilege — the matrix reads left to right as "and also".
const COLUMNS = ["viewer", "editor", "admin"] as const;
type RoleName = (typeof COLUMNS)[number];

const RANK: Record<string, number> = { viewer: 1, editor: 2, admin: 3 };

// Whether SSO is configured — the same question the login page answers
// itself from this same endpoint. Gates OIDCMappingPanel below: the hub only
// registers its routes when OIDC is actually configured (router.go), so
// mounting the panel unconditionally would show nothing but 404s on an
// install with no IdP wired up. Instance-global and cheap, so cache it hard.
function useAuthConfig() {
  return useQuery({
    queryKey: queryKeys.authConfig,
    queryFn: () => apiGet<AuthConfig>("/api/v1/auth/config"),
    staleTime: 5 * 60_000,
  });
}

// Who can do what. Every cell comes from the hub, which derives it from the
// authorization its routes actually registered with — a hand-written table
// here would be a second copy of the rules and would be wrong the first time
// one of them changed, in the direction that matters least until it matters
// most.
export function AccessTab() {
  const { data, isLoading, isError } = usePermissions();
  const { data: authConfig } = useAuthConfig();
  const ssoConfigured = !!authConfig?.methods.includes("oidc");

  if (isLoading) return <CenteredSpinner />;
  if (isError || !data) {
    return (
      <Card className="p-8 text-center text-sm text-error">
        Couldn’t reach the hub to read the permission model.
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {!data.authEnabled && (
        <Card className="flex items-start gap-3 border-warning/40 p-4 text-sm">
          <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
          <div className="flex flex-col gap-1">
            <span className="font-medium">Authentication is off on this install.</span>
            <span className="text-base-content/60">
              Every request is served without a session, so the roles below are
              not being enforced — this is what would apply once you set{" "}
              <code className="rounded bg-base-300 px-1">auth.enabled=true</code>.
            </span>
          </div>
        </Card>
      )}

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>Roles</CardTitle>
        </CardHeader>
        <ul className="divide-y divide-neutral" data-testid="role-list">
          {data.roles.map((r) => (
            <RoleRow key={r.role} r={r} />
          ))}
        </ul>
      </Card>

      <Card className="overflow-hidden">
        <CardHeader>
          <CardTitle>What each role can do</CardTitle>
        </CardHeader>
        <div className="overflow-x-auto">
          <table className="table-dense w-full text-sm" data-testid="permission-matrix">
            <thead>
              <tr className="border-y border-neutral text-left">
                <th>Area</th>
                {COLUMNS.map((role) => (
                  <th key={role} className="text-center capitalize">
                    {role}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.areas.map((a) => (
                <AreaRow key={a.area} a={a} />
              ))}
            </tbody>
          </table>
        </div>
        <p className="border-t border-neutral px-4 py-2 text-xs text-base-content/45">
          Read and write are granted <strong>per project</strong> — a viewer on
          one project sees nothing of another. Administration is global: it is
          not something an admin grant on a single project confers. Assign roles
          in <span className="font-mono">Settings → Users</span>, or map them
          from your identity provider’s groups.
        </p>
      </Card>

      {/* Personal API tokens need an identity to own them — with auth off
          every request is anonymous, so the card would only mint credentials
          that mean nothing. */}
      {data.authEnabled && <ApiTokensCard />}

      {ssoConfigured && <OIDCMappingPanel />}
    </div>
  );
}

function RoleRow({ r }: { r: PermissionRole }) {
  return (
    <li className="flex flex-col gap-1 px-4 py-3">
      <Badge tone={r.role === "admin" ? "primary" : "neutral"}>{r.label}</Badge>
      <span className="text-sm text-base-content/70">{r.description}</span>
    </li>
  );
}

function AreaRow({ a }: { a: PermissionArea }) {
  return (
    <tr className="border-b border-neutral/50 last:border-0">
      <td className="font-medium">{a.label}</td>
      {COLUMNS.map((role) => (
        <td key={role} className="text-center" data-testid={`cell-${a.area}-${role}`}>
          <Cell role={role} area={a} />
        </td>
      ))}
    </tr>
  );
}

// A cell says the strongest thing the role can do in that area. "Read" without
// a write marker is not an omission: most areas have no write route at all,
// and showing a dash for "nobody can" would read the same as "you can't".
function Cell({ role, area }: { role: RoleName; area: PermissionArea }) {
  const can = (need?: string) => !!need && RANK[role] >= (RANK[need] ?? 99);
  if (can(area.write)) {
    return (
      <span className="inline-flex items-center gap-1 text-success">
        <Check className="h-3.5 w-3.5" /> Read &amp; change
      </span>
    );
  }
  if (can(area.read)) {
    return (
      <span className="inline-flex items-center gap-1 text-base-content/70">
        <Check className="h-3.5 w-3.5" /> Read
      </span>
    );
  }
  return <Minus className="mx-auto h-3.5 w-3.5 text-base-content/25" aria-label="no access" />;
}
