import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { ProfilingScreen } from "@/components/profiling/profiling-screen";

export default function ProfilingPage() {
  return (
    <>
      <Topbar />
      <main className="flex flex-1 flex-col overflow-y-auto p-5">
        <ModuleGate module="profiling">
          {/* useSearchParams consumers must sit under Suspense (static export) */}
          <Suspense fallback={<CenteredSpinner />}>
            <ProfilingScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
