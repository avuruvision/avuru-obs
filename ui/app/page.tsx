"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

// The product opens on the Dashboard: the estate at a glance is the answer to
// "what do I look at first". Traces (heatmap-first) held this slot until v0.5
// gave the install a single overview screen.
export default function Home() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/dashboard");
  }, [router]);
  return null;
}
