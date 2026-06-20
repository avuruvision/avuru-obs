import { Gauge } from "lucide-react";
import { Topbar } from "@/components/layout/topbar";
import { ComingSoon } from "@/components/ui/coming-soon";

export default function MetricsPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ComingSoon icon={Gauge} title="Metrics" milestone="M3">
          RED metrics and resource usage per service and workload, derived from
          the same OTLP/eBPF signals — no separate Prometheus to operate.
        </ComingSoon>
      </main>
    </>
  );
}
