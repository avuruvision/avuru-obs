import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { CenteredSpinner } from "@/components/ui/spinner";
import { LogsScreen } from "@/components/logs/logs-screen";

export default function LogsPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        {/* useSearchParams consumers must sit under Suspense (static export) */}
        <Suspense fallback={<CenteredSpinner />}>
          <LogsScreen />
        </Suspense>
      </main>
    </>
  );
}
