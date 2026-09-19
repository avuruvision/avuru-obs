"use client";

import { useEffect, useRef, useState } from "react";
import { CalendarRange, Clock } from "lucide-react";
import { useTimeRange, RANGE_PRESETS, type RangePreset } from "@/hooks/use-time-range";
import { cn } from "@/lib/cn";

// One global time range for every screen (agent_docs/ui_patterns.md rule 4),
// held in the URL (?range=) so views stay pasteable, with localStorage
// carrying it across navigations (see TimeRangeSync). Beside the presets, an
// absolute window: two local date-times, sent to the hub as RFC 3339.
export function TimeRangePicker() {
  const { preset, custom, setPreset, setCustom } = useTimeRange();
  const [open, setOpen] = useState(false);

  return (
    <div className="relative flex items-center gap-1 rounded-lg border border-neutral bg-base-200 p-0.5">
      <Clock className="ml-2 h-3.5 w-3.5 text-base-content/75" aria-hidden />
      {(Object.keys(RANGE_PRESETS) as RangePreset[]).map((p) => (
        <button
          key={p}
          onClick={() => { setPreset(p); setOpen(false); }}
          aria-pressed={!custom && p === preset}
          className={cn(
            "rounded-md px-2 py-1 text-xs font-medium transition-colors",
            !custom && p === preset
              ? "bg-primary/15 text-primary"
              : "text-base-content/75 hover:text-base-content",
          )}
        >
          {p}
        </button>
      ))}
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-pressed={Boolean(custom)}
        aria-expanded={open}
        aria-label="Custom time range"
        title={custom ? `${custom.from} → ${custom.to} (UTC)` : "Pick an absolute date and time range"}
        className={cn(
          "inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors",
          custom ? "bg-primary/15 text-primary" : "text-base-content/75 hover:text-base-content",
        )}
      >
        <CalendarRange className="h-3.5 w-3.5" aria-hidden />
        {custom ? formatWindow(custom.from, custom.to) : "Custom"}
      </button>
      {open && (
        <CustomRangeForm
          from={custom?.from}
          to={custom?.to}
          onApply={(from, to) => {
            if (!setCustom(from, to)) return false;
            setOpen(false);
            return true;
          }}
          onClose={() => setOpen(false)}
        />
      )}
    </div>
  );
}

// "Sep 18 15:00 → 16:30" in the viewer's locale; the day repeats only when the
// window crosses one.
function formatWindow(from: string, to: string): string {
  const a = new Date(from);
  const b = new Date(to);
  const day = (d: Date) => d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  const clock = (d: Date) => d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  const sameDay = day(a) === day(b);
  return `${day(a)} ${clock(a)} → ${sameDay ? "" : `${day(b)} `}${clock(b)}`;
}

// A datetime-local value is the viewer's wall clock with no zone, so it
// round-trips through Date, which reads it as local time.
function toLocalInput(iso?: string): string {
  const d = iso ? new Date(iso) : new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function CustomRangeForm({
  from,
  to,
  onApply,
  onClose,
}: {
  from?: string;
  to?: string;
  onApply: (from: string, to: string) => boolean;
  onClose: () => void;
}) {
  const [start, setStart] = useState(() => toLocalInput(from ?? new Date(Date.now() - RANGE_PRESETS["1h"]).toISOString()));
  const [end, setEnd] = useState(() => toLocalInput(to));
  const [error, setError] = useState<string | null>(null);
  const box = useRef<HTMLFormElement>(null);

  // A click outside or Escape closes the popover without applying.
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [onClose]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const a = new Date(start);
    const b = new Date(end);
    if (Number.isNaN(a.getTime()) || Number.isNaN(b.getTime())) return setError("Both ends need a date and a time");
    if (b <= a) return setError("The end must come after the start");
    if (!onApply(a.toISOString(), b.toISOString())) setError("The end must come after the start");
  };

  return (
    <form
      ref={box}
      onSubmit={submit}
      noValidate
      aria-label="Absolute time range"
      className="absolute right-0 top-full z-30 mt-1 flex w-72 flex-col gap-2 rounded-lg border border-neutral bg-base-200 p-3 text-xs [box-shadow:var(--shadow-card)]"
    >
      <label className="flex flex-col gap-1">
        <span className="text-base-content/70">From</span>
        <input type="datetime-local" value={start} max={end} step={60} required onChange={(e) => setStart(e.target.value)} aria-label="Range start" className="h-8 rounded-md border border-neutral bg-base-100 px-2 text-sm" />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-base-content/70">To</span>
        <input type="datetime-local" value={end} min={start} step={60} required onChange={(e) => setEnd(e.target.value)} aria-label="Range end" className="h-8 rounded-md border border-neutral bg-base-100 px-2 text-sm" />
      </label>
      {error && <p role="alert" className="text-error">{error}</p>}
      <div className="flex items-center justify-between">
        <span className="text-base-content/55">Local time</span>
        <button type="submit" className="rounded-md bg-primary px-3 py-1 font-medium text-primary-content hover:opacity-90">Apply</button>
      </div>
    </form>
  );
}
