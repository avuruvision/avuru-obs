"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { CenteredSpinner } from "@/components/ui/spinner";

// User administration now lives as an in-place tab on the Settings screen
// (/settings?tab=users) so the tab bar never disappears. This route is kept as
// a redirect so old links and bookmarks still resolve.
export default function UsersPage() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/settings?tab=users");
  }, [router]);
  return <CenteredSpinner />;
}
