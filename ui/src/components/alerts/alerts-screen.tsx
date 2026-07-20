"use client";

import { BellRing } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { CenteredSpinner } from "@/components/ui/spinner";
import { useAlerts, useAlertRules } from "@/hooks/use-alerts-data";
import { FiringList } from "./firing-list";
import { HistoryTimeline } from "./history-timeline";
import { RulesList } from "./rules-list";
import { ChannelsPanel } from "./channels-panel";

// Alerts board: what's firing now, the recent fire/resolve timeline, the
// read-only rule list, and the notification channels. Rules are edited in
// config (a ConfigMap the hub hot-reloads); channels are editable HERE — the
// panel renders even in the no-rules empty state so delivery can be set up
// first.
export function AlertsScreen() {
  const alerts = useAlerts();
  const rules = useAlertRules();

  if (alerts.isLoading || rules.isLoading) return <CenteredSpinner />;

  const firing = alerts.data?.firing ?? [];
  const history = alerts.data?.history ?? [];
  const ruleList = rules.data?.rules ?? [];

  if (!ruleList.length && !firing.length && !history.length) {
    return (
      <div className="flex flex-col gap-5">
        <EmptyState icon={BellRing} title="No alerting rules yet">
          Alerts fire from your <strong>service-health</strong> status. Define
          rules in the chart&apos;s <code className="rounded bg-base-300 px-1">alerting</code>{" "}
          config (a ConfigMap the hub hot-reloads) — e.g. fire a webhook when the{" "}
          <code className="rounded bg-base-300 px-1">payments</code> group is{" "}
          <code className="rounded bg-base-300 px-1">down</code> for 5m. Rules
          reference the channels below by name.
        </EmptyState>
        <ChannelsPanel />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-5">
      <FiringList firing={firing} />
      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <HistoryTimeline history={history} />
        <RulesList rules={ruleList} />
      </div>
      <ChannelsPanel />
    </div>
  );
}
