import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { CenteredSpinner } from "@/components/ui/spinner";
import { ServiceMapScreen } from "@/components/service-map/service-map-screen";

export default function ServiceMapPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        {/* useTimeRange (useSearchParams) consumer — Suspense for static export */}
        <Suspense fallback={<CenteredSpinner />}>
          <ServiceMapScreen />
        </Suspense>
      </main>
    </>
  );
}
