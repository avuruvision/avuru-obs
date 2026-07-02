import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { CenteredSpinner } from "@/components/ui/spinner";
import { ProfilingScreen } from "@/components/profiling/profiling-screen";

export default function ProfilingPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        {/* useSearchParams consumers must sit under Suspense (static export) */}
        <Suspense fallback={<CenteredSpinner />}>
          <ProfilingScreen />
        </Suspense>
      </main>
    </>
  );
}
