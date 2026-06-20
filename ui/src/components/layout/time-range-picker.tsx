"use client";

import { Clock } from "lucide-react";
import { useTimeRange, RANGE_PRESETS, type RangePreset } from "@/hooks/use-time-range";
import { cn } from "@/lib/cn";

// One global time range for every screen (agent_docs/ui_patterns.md rule 4),
// held in the URL (?range=) so views stay pasteable.
export function TimeRangePicker() {
  const { preset, setPreset } = useTimeRange();

  return (
    <div className="flex items-center gap-1 rounded-lg border border-neutral bg-base-200 p-0.5">
      <Clock className="ml-2 h-3.5 w-3.5 text-base-content/50" aria-hidden />
      {(Object.keys(RANGE_PRESETS) as RangePreset[]).map((p) => (
        <button
          key={p}
          onClick={() => setPreset(p)}
          aria-pressed={p === preset}
          className={cn(
            "rounded-md px-2 py-1 text-xs font-medium transition-colors",
            p === preset
              ? "bg-primary/15 text-primary"
              : "text-base-content/60 hover:text-base-content",
          )}
        >
          {p}
        </button>
      ))}
    </div>
  );
}
