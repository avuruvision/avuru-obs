import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { AIScreen } from "@/components/ai/ai-screen";

export default function AIPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ModuleGate module="ai">
          <Suspense fallback={<CenteredSpinner />}>
            <AIScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
