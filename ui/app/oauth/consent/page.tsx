"use client";

import { Suspense } from "react";
import { ConsentCard } from "@/components/oauth/consent-card";
import { CenteredSpinner } from "@/components/ui/spinner";

// The consent screen lives in the UI because the hub is API-only
// (agent_docs/architecture.md) — it serves no HTML at all. The hub exposes the
// decision as JSON and this page is its client, like every other screen.
export default function OAuthConsentPage() {
  return (
    <Suspense fallback={<CenteredSpinner />}>
      <ConsentCard />
    </Suspense>
  );
}
