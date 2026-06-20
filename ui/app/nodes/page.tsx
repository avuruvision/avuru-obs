import { Server } from "lucide-react";
import { Topbar } from "@/components/layout/topbar";
import { ComingSoon } from "@/components/ui/coming-soon";

export default function NodesPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ComingSoon icon={Server} title="Node & pod health" milestone="M3">
          CPU, memory, and network per node and pod (kubeletstats) — answer
          “is it the app or the node?” without leaving the incident.
        </ComingSoon>
      </main>
    </>
  );
}
