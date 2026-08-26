"use client";

import { Tabs, type TabItem } from "@/components/ui/tabs";
import { useURLState } from "@/hooks/use-url-state";
import { useAuth } from "@/hooks/use-auth";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { SystemStatus } from "./system-status";
import { GeneralTab } from "./general-tab";
import { CollectionSettings } from "./collection-settings";
import { AccountTab } from "./account-tab";
import { UsersPanel } from "./users-panel";
import { ServiceGroupsPanel } from "./service-groups-panel";
import { StorageTab } from "./storage-tab";
import { AccessTab } from "./access-tab";

const TABS = [
  "general",
  "collection",
  "groups",
  "storage",
  "access",
  "status",
  "account",
  "users",
] as const;
type Tab = (typeof TABS)[number];

// Coroot-inspired settings: General (project), Collection (agents), Status
// (instance), Account (your own identity) and Users (admin-only). Every tab
// swaps in place so the tab bar stays put — "users" used to route to its own
// page, which made the whole bar vanish. Tab state lives in the URL (?tab=) —
// shareable like everything else. Requires a Suspense boundary in the page
// (useSearchParams).
export function SettingsScreen() {
  const { get, setMany } = useURLState();
  const { me, isAdmin, canAdminister } = useAuth();
  // Groups belong to the service-health module; the tab follows it, like the
  // sidebar entries do.
  const healthEnabled = useModuleEnabled("service-health");
  // "account" needs a real session — it's self-service credential management,
  // so the anonymous fallback (and auth-off, where /auth/me 404s and me stays
  // null) has nothing to manage.
  const signedIn = me !== null && !me.user.anonymous;
  // "users" is admin-only; anyone else requesting either tab falls back to
  // general (matches the hub, which answers 403 on /api/v1/users to non-admins).
  //
  // "groups", "storage" and "status" are administration too, and are gated on
  // canAdminister rather than isAdmin — false there means "no admin grant",
  // which is also true on an auth-DISABLED install where anyone may use them,
  // so isAdmin alone would hide them from exactly those installs.
  //   · groups is a configuration editor. A viewer cannot change a group, and
  //     the grouping it edits is already shown where it is read — the health
  //     board's tier lanes. Offering the read-only editor to the shared demo
  //     account was the visible half of that: a settings screen full of
  //     controls it exists to not have.
  //   · storage and status both read /api/v1/system/status, which is
  //     securedAdmin. Shown to a viewer they rendered "couldn't reach the
  //     hub" — an outage that was not happening, in place of a refusal that
  //     was.
  // "access" stays open on purpose — understanding a refusal is not a
  // privilege, and the hub serves the matrix to any caller.
  const requested = get("tab");
  const tab = (TABS.find(
    (t) =>
      t === requested &&
      (t !== "users" || isAdmin) &&
      (t !== "account" || signedIn) &&
      (t !== "groups" || (healthEnabled && canAdminister)) &&
      (t !== "storage" || canAdminister) &&
      (t !== "status" || canAdminister),
  ) ?? "general") as Tab;

  const items: TabItem<Tab>[] = [
    { value: "general", label: "General" },
    { value: "collection", label: "Collection" },
    ...(healthEnabled && canAdminister ? [{ value: "groups" as const, label: "Groups" }] : []),
    ...(canAdminister ? [{ value: "storage" as const, label: "Storage" }] : []),
    { value: "access", label: "Access" },
    ...(canAdminister ? [{ value: "status" as const, label: "Status" }] : []),
    ...(signedIn ? [{ value: "account" as const, label: "Account" }] : []),
    ...(isAdmin ? [{ value: "users" as const, label: "Users" }] : []),
  ];

  return (
    <div className="flex flex-col gap-4">
      <Tabs<Tab>
        value={tab}
        onChange={(t) => setMany({ tab: t === "general" ? undefined : t })}
        items={items}
      />
      {tab === "general" && <GeneralTab />}
      {tab === "collection" && <CollectionSettings />}
      {tab === "groups" && <ServiceGroupsPanel />}
      {tab === "storage" && (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-base-content/45">
            The install as a whole (storage is shared), and what the selected
            project holds inside it.
          </p>
          <StorageTab />
        </div>
      )}
      {tab === "access" && <AccessTab />}
      {tab === "status" && (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-base-content/45">
            Instance-wide (all projects) — storage and ingestion are shared.
          </p>
          <SystemStatus />
        </div>
      )}
      {tab === "account" && <AccountTab />}
      {tab === "users" && <UsersPanel />}
    </div>
  );
}
