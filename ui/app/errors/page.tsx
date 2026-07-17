import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { ModuleGate } from "@/components/layout/module-gate";
import { CenteredSpinner } from "@/components/ui/spinner";
import { ErrorsScreen } from "@/components/errors/errors-screen";

export default function ErrorsPage() {
  return (
    <>
      <Topbar />
      <main className="flex flex-1 flex-col overflow-hidden p-5">
        <ModuleGate module="error-tracking">
          {/* useSearchParams consumers must sit under Suspense (static export) */}
          <Suspense fallback={<CenteredSpinner />}>
            <ErrorsScreen />
          </Suspense>
        </ModuleGate>
      </main>
    </>
  );
}
