import { Map as MapIcon } from "lucide-react";
import { Topbar } from "@/components/layout/topbar";
import { ComingSoon } from "@/components/ui/coming-soon";

export default function ServiceMapPage() {
  return (
    <>
      <Topbar title="Service Map" />
      <main className="flex-1 overflow-y-auto p-5">
        <ComingSoon icon={MapIcon} title="Live service map" milestone="M2">
          The wedge: a live topology built from eBPF TCP flow tracing — no SDK,
          no sidecars, no YAML. Install the sensor and watch your architecture
          draw itself in under five minutes.
        </ComingSoon>
      </main>
    </>
  );
}
