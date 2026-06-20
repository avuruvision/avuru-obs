import { Boxes } from "lucide-react";
import { Topbar } from "@/components/layout/topbar";
import { ComingSoon } from "@/components/ui/coming-soon";

export default function ServicesPage() {
  return (
    <>
      <Topbar title="Services" />
      <main className="flex-1 overflow-y-auto p-5">
        <ComingSoon icon={Boxes} title="Service inventory" milestone="M2">
          Every service in your cluster, auto-discovered by eBPF with zero code
          changes — RED metrics, dependencies, and health at a glance. Until
          then, the Traces screen already shows services sending OTLP.
        </ComingSoon>
      </main>
    </>
  );
}
