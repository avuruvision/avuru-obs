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
  const { me, isAdmin } = useAuth();
  // Groups belong to the service-health module; the tab follows it, like the
  // sidebar entries do.
  const healthEnabled = useModuleEnabled("service-health");
  // "account" needs a real session — it's self-service credential management,
  // so the anonymous fallback (and auth-off, where /auth/me 404s and me stays
  // null) has nothing to manage.
  const signedIn = me !== null && !me.user.anonymous;
  // "users" is admin-only; anyone else requesting either tab falls back to
  // general (matches the hub, which answers 403 on /api/v1/users to non-admins).
  // "groups" is readable by anyone — the panel itself hides the write controls
  // from non-admins, matching the hub's viewer-read/admin-write split. It is
  // gated on the service-health MODULE, not on a role.
  // "storage" and "status" read the same admin-only endpoint and are gated the
  // same way: not at all. isAdmin is false on an auth-DISABLED install (there
  // is no /auth/me to read a grant from), so gating on it would hide them from
  // exactly the installs where anyone may use them. "access" is open on
  // purpose — understanding a refusal is not a privilege, and the hub serves
  // the matrix to any caller.
  const requested = get("tab");
  const tab = (TABS.find(
    (t) =>
      t === requested &&
      (t !== "users" || isAdmin) &&
      (t !== "account" || signedIn) &&
      (t !== "groups" || healthEnabled),
  ) ?? "general") as Tab;

  const items: TabItem<Tab>[] = [
    { value: "general", label: "General" },
    { value: "collection", label: "Collection" },
    ...(healthEnabled ? [{ value: "groups" as const, label: "Groups" }] : []),
    { value: "storage", label: "Storage" },
    { value: "access", label: "Access" },
    { value: "status", label: "Status" },
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
            Instance-wide (all projects) — storage is shared.
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
