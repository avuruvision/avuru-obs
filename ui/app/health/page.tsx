import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { HealthScreen } from "@/components/health/health-screen";

export default function HealthPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ModuleGate module="service-health">
          {/* useSearchParams consumers must sit under Suspense (static export) */}
          <Suspense fallback={<CenteredSpinner />}>
            <HealthScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
