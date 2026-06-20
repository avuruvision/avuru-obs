"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

// The product opens on Traces (heatmap-first overview).
export default function Home() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/traces");
  }, [router]);
  return null;
}
