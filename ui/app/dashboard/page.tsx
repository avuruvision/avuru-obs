import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { CenteredSpinner } from "@/components/ui/spinner";
import { DashboardScreen } from "@/components/dashboard/dashboard-screen";

// No ModuleGate: the Dashboard is core and is the landing route, so it must
// render on every install. Its bands gate themselves per module.
export default function DashboardPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <Suspense fallback={<CenteredSpinner />}>
          <DashboardScreen />
        </Suspense>
      </main>
    </>
  );
}
