import { Leaf } from "lucide-react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { EmptyState } from "@/components/ui/empty-state";

export default function GreenPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ModuleGate module="green">
          <EmptyState icon={Leaf} title="Energy & carbon">
            The green dashboard — per-service energy (Wh) and carbon (gCO2e),
            per-request intensity, and carbon budgets — lands here in an
            upcoming release.
          </EmptyState>
        </ModuleGate>
      </main>
    </>
  );
}
