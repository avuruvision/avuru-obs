"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

// Explorer opens on relationships; the dashboard remains available in navigation.
export default function Home() {
  const router = useRouter();
  useEffect(() => {
    router.replace(`/service-map${window.location.search}`);
  }, [router]);
  return null;
}
