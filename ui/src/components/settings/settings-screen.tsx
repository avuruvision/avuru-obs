"use client";

import { Tabs, type TabItem } from "@/components/ui/tabs";
import { useURLState } from "@/hooks/use-url-state";
import { useAuth } from "@/hooks/use-auth";
import { SystemStatus } from "./system-status";
import { GeneralTab } from "./general-tab";
import { CollectionSettings } from "./collection-settings";
import { UsersPanel } from "./users-panel";

const TABS = ["general", "collection", "status", "users"] as const;
type Tab = (typeof TABS)[number];

// Coroot-inspired settings: General (project), Collection (agents), Status
// (instance), and Users (admin-only). Every tab swaps in place so the tab bar
// stays put — "users" used to route to its own page, which made the whole bar
// vanish. Tab state lives in the URL (?tab=) — shareable like everything else.
// Requires a Suspense boundary in the page (useSearchParams).
export function SettingsScreen() {
  const { get, setMany } = useURLState();
  const { isAdmin } = useAuth();
  // "users" is admin-only; anyone else requesting it falls back to general
  // (matches the hub, which answers 403 on /api/v1/users to non-admins).
  const requested = get("tab");
  const tab = (TABS.find(
    (t) => t === requested && (t !== "users" || isAdmin),
  ) ?? "general") as Tab;

  const items: TabItem<Tab>[] = [
    { value: "general", label: "General" },
    { value: "collection", label: "Collection" },
    { value: "status", label: "Status" },
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
      {tab === "status" && (
        <div className="flex flex-col gap-2">
          <p className="text-xs text-base-content/45">
            Instance-wide (all projects) — storage and ingestion are shared.
          </p>
          <SystemStatus />
        </div>
      )}
      {tab === "users" && <UsersPanel />}
    </div>
  );
}
