import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { GreenScreen } from "@/components/green/green-screen";

export default function GreenPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ModuleGate module="green">
          <Suspense fallback={<CenteredSpinner />}>
            <GreenScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
