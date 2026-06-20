import { Flame } from "lucide-react";
import { Topbar } from "@/components/layout/topbar";
import { ComingSoon } from "@/components/ui/coming-soon";

export default function ProfilingPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ComingSoon icon={Flame} title="Continuous profiling" milestone="M4">
          CPU flame graphs per service, always on, powered by the OpenTelemetry
          eBPF profiler — see exactly which function burns the CPU behind a
          slow span.
        </ComingSoon>
      </main>
    </>
  );
}
