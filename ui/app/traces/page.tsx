import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { CenteredSpinner } from "@/components/ui/spinner";
import { TracesScreen } from "@/components/traces/traces-screen";

export default function TracesPage() {
  return (
    <>
      <Topbar title="Traces" />
      <main className="flex-1 overflow-y-auto p-5">
        {/* useSearchParams consumers must sit under Suspense (static export) */}
        <Suspense fallback={<CenteredSpinner />}>
          <TracesScreen />
        </Suspense>
      </main>
    </>
  );
}
