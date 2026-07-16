import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { LogsScreen } from "@/components/logs/logs-screen";

export default function LogsPage() {
  return (
    <>
      <Topbar />
      <main className="flex flex-1 flex-col overflow-y-auto p-5">
        <ModuleGate module="logs">
          {/* useSearchParams consumers must sit under Suspense (static export) */}
          <Suspense fallback={<CenteredSpinner />}>
            <LogsScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
