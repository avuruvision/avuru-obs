"use client";

import { Tabs } from "@/components/ui/tabs";
import { useURLState } from "@/hooks/use-url-state";
import { SystemStatus } from "./system-status";
import { GeneralTab } from "./general-tab";
import { CollectionSettings } from "./collection-settings";

const TABS = ["general", "collection", "status"] as const;
type Tab = (typeof TABS)[number];

// Coroot-inspired settings: General (project), Collection (agents), Status
// (instance). Tab state lives in the URL (?tab=) — shareable like everything
// else. Requires a Suspense boundary in the page (useSearchParams).
export function SettingsScreen() {
  const { get, setMany } = useURLState();
  const tab = (TABS.find((t) => t === get("tab")) ?? "general") as Tab;

  return (
    <div className="flex flex-col gap-4">
      <Tabs<Tab>
        value={tab}
        onChange={(t) => setMany({ tab: t === "general" ? undefined : t })}
        items={[
          { value: "general", label: "General" },
          { value: "collection", label: "Collection" },
          { value: "status", label: "Status" },
        ]}
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
    </div>
  );
}
