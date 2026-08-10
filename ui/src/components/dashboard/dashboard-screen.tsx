"use client";

import { LayoutDashboard } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { SummaryBand } from "./summary-band";
import { TopologyCard } from "./topology-card";
import { AlertsCard } from "./alerts-card";
import { CapacityBandLive } from "./capacity-band";

// The Dashboard: one fixed screen answering "how is the estate doing?".
//
// Fixed on purpose (v0.5 plan, W6): no widget model, no layout editor, no
// persistence. Every band reads an API that already exists, so this screen adds
// no hub surface at all. User-composable dashboards are a separate release.
//
// The screen itself is core — it is the landing route, so it must render on
// every install. Each band instead degrades on its own: bands whose module is
// off simply don't mount (see the module gates below), and the screen never
// shows a band that would 404.
export function DashboardScreen() {
  const { time } = useTimeRange();
  const { data, isLoading } = useServiceMapData(time);
  const alertingEnabled = useModuleEnabled("alerting");
  const infraEnabled = useModuleEnabled("infra-metrics");

  if (isLoading) return <CenteredSpinner />;

  const services = data?.services ?? [];
  const edges = data?.edges ?? [];

  // No telemetry at all: teach rather than draw six empty cards. Mirrors the
  // service map's empty state, which is where a new install starts.
  if (!services.length) {
    return (
      <EmptyState icon={LayoutDashboard} title="No telemetry yet">
        This screen summarizes the estate: service health, topology, firing
        alerts and Kubernetes capacity. Point an OTel SDK at the gateway — or
        install the sensor — and it fills in as data arrives.
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-6" data-testid="dashboard-screen">
      <SummaryBand services={services} />

      <section className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        {/* The map earns the width; alerts are a list and read fine narrow.
            With alerting off the map simply takes the full row. */}
        <div className={alertingEnabled ? "lg:col-span-2" : "lg:col-span-3"}>
          <TopologyCard services={services} edges={edges} />
        </div>
        {alertingEnabled && <AlertsCard />}
      </section>

      {infraEnabled && <CapacityBandLive />}
    </div>
  );
}
