"use client";

import { useState } from "react";
import { Settings } from "lucide-react";
import { Tabs } from "@/components/ui/tabs";
import { ComingSoon } from "@/components/ui/coming-soon";
import { SystemStatus } from "./system-status";

type Tab = "status" | "configuration";

export function SettingsScreen() {
  const [tab, setTab] = useState<Tab>("status");

  return (
    <div className="flex flex-col gap-4">
      <Tabs<Tab>
        value={tab}
        onChange={setTab}
        items={[
          { value: "status", label: "Status" },
          { value: "configuration", label: "Configuration" },
        ]}
      />
      {tab === "status" ? (
        <SystemStatus />
      ) : (
        <ComingSoon icon={Settings} title="Configuration" milestone="M5">
          Authentication, retention policies, agent configuration (OpAMP) — the
          control plane gets its controls in the hardening milestone.
        </ComingSoon>
      )}
    </div>
  );
}
