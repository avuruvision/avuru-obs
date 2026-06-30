"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { TraceResponse } from "@/lib/api-types";

// Raw, copyable trace JSON — Jaeger's "Trace JSON".
export function TraceJson({ trace }: { trace: TraceResponse }) {
  const [copied, setCopied] = useState(false);
  const json = JSON.stringify(trace, null, 2);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(json);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard blocked (insecure context) — no-op
    }
  };

  return (
    <div className="relative">
      <Button
        variant="secondary"
        size="sm"
        onClick={copy}
        className="absolute right-3 top-3"
      >
        {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
        {copied ? "Copied" : "Copy"}
      </Button>
      <pre className="max-h-[72vh] overflow-auto rounded-lg border border-neutral bg-base-200 p-4 font-mono text-xs leading-relaxed">
        {json}
      </pre>
    </div>
  );
}
