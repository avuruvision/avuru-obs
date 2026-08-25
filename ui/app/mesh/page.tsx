import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { MeshScreen } from "@/components/mesh/mesh-screen";

export default function MeshPage() {
  return (
    <>
      <Topbar />
      <main className="flex flex-1 flex-col overflow-y-auto p-5">
        <ModuleGate module="mesh">
          {/* useSearchParams consumers must sit under Suspense (static export) */}
          <Suspense fallback={<CenteredSpinner />}>
            <MeshScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
