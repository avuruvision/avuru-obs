"use client";

import { ShieldAlert } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CenteredSpinner } from "@/components/ui/spinner";
import { apiGet } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import { usePermissions } from "@/hooks/use-permissions";
import { ApiTokensCard } from "./api-tokens-card";
import { ConnectedAppsCard } from "./connected-apps-card";
import { OIDCMappingPanel } from "./oidc-mapping-panel";
import { PermissionMatrix } from "./permission-matrix";
import type { AuthConfig, PermissionRole } from "@/lib/api-types";

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

// The Access tab: who the roles are, what each one can do (PermissionMatrix),
// and the credentials the signed-in user holds against the API.
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

      <PermissionMatrix areas={data.areas} />

      {/* Personal API tokens need an identity to own them — with auth off
          every request is anonymous, so the card would only mint credentials
          that mean nothing. */}
      {data.authEnabled && <ApiTokensCard />}
      {/* Renders nothing when no application is connected, which is every
          install with OAuth off. */}
      {data.authEnabled && <ConnectedAppsCard />}

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
