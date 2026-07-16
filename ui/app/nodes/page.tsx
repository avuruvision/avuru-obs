import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { NodesScreen } from "@/components/infra/nodes-screen";

export default function NodesPage() {
  return (
    <>
      <Topbar />
      <main className="flex flex-1 flex-col overflow-y-auto p-5">
        <ModuleGate module="infra-metrics">
          {/* useSearchParams consumers must sit under Suspense (static export) */}
          <Suspense fallback={<CenteredSpinner />}>
            <NodesScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
