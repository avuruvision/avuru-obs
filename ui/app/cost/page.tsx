import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { CostScreen } from "@/components/cost/cost-screen";

export default function CostPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ModuleGate module="cost">
          <Suspense fallback={<CenteredSpinner />}>
            <CostScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
