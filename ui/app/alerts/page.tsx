import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { AlertsScreen } from "@/components/alerts/alerts-screen";

export default function AlertsPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ModuleGate module="alerting">
          <Suspense fallback={<CenteredSpinner />}>
            <AlertsScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
