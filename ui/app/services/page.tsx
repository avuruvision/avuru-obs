import { Suspense } from "react";
import { Topbar } from "@/components/layout/topbar";
import { CenteredSpinner } from "@/components/ui/spinner";
import { ServicesScreen } from "@/components/services/services-screen";

export default function ServicesPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        {/* useSearchParams consumers must sit under Suspense (static export) */}
        <Suspense fallback={<CenteredSpinner />}>
          <ServicesScreen />
        </Suspense>
      </main>
    </>
  );
}
