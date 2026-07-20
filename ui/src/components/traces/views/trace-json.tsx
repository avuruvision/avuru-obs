"use client";

import { CopyButton } from "@/components/ui/copy-button";
import type { TraceResponse } from "@/lib/api-types";

// Raw, copyable trace JSON.
export function TraceJson({ trace }: { trace: TraceResponse }) {
  const json = JSON.stringify(trace, null, 2);
  return (
    <div className="relative">
      <CopyButton
        value={json}
        label="Copy"
        ariaLabel="Copy trace JSON"
        className="absolute right-3 top-3 rounded-md border border-neutral bg-base-100 px-2 py-1 hover:bg-base-300"
      />
      <pre className="max-h-[72vh] overflow-auto rounded-lg border border-neutral bg-base-200 p-4 font-mono text-xs leading-relaxed">
        {json}
      </pre>
    </div>
  );
}
