"use client";

import { useRouter } from "next/navigation";
import { Tabs, type TabItem } from "@/components/ui/tabs";
import { useURLState } from "@/hooks/use-url-state";
import { useAuth } from "@/hooks/use-auth";
import { SystemStatus } from "./system-status";
import { GeneralTab } from "./general-tab";
import { CollectionSettings } from "./collection-settings";

const TABS = ["general", "collection", "status"] as const;
type Tab = (typeof TABS)[number];
// "users" is a routing-only tab: it lives at its own page (/settings/users)
// rather than swapping in place, and shows only to admins.
type NavTab = Tab | "users";

// Coroot-inspired settings: General (project), Collection (agents), Status
// (instance). Tab state lives in the URL (?tab=) — shareable like everything
// else. Requires a Suspense boundary in the page (useSearchParams).
export function SettingsScreen() {
  const { get, setMany } = useURLState();
  const { isAdmin } = useAuth();
  const router = useRouter();
  const tab = (TABS.find((t) => t === get("tab")) ?? "general") as Tab;

  const items: TabItem<NavTab>[] = [
    { value: "general", label: "General" },
    { value: "collection", label: "Collection" },
    { value: "status", label: "Status" },
    ...(isAdmin ? [{ value: "users" as const, label: "Users" }] : []),
  ];

  return (
    <div className="flex flex-col gap-4">
      <Tabs<NavTab>
        value={tab}
        onChange={(t) => {
          if (t === "users") {
            router.push("/settings/users");
            return;
          }
          setMany({ tab: t === "general" ? undefined : t });
        }}
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
    </div>
  );
}
