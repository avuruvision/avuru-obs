import { cn } from "@/lib/cn";

// One labelled number in a stat strip. The tone is for numbers that mean
// trouble when they are not zero — a regression count reads differently from a
// service count, and the colour is what makes that legible at a glance.
export function Stat({
  label,
  value,
  sub,
  tone,
  testid,
}: {
  label: string;
  value: string;
  sub?: string;
  tone?: "error" | "warning";
  testid?: string;
}) {
  return (
    <div className="bg-base-200 p-3" data-testid={testid}>
      <p className="text-xs uppercase tracking-wider text-base-content/50">{label}</p>
      <p
        data-testid={testid ? `${testid}-value` : undefined}
        className={cn(
          "text-sm font-semibold",
          tone === "error" && "text-error",
          tone === "warning" && "text-warning",
        )}
      >
        {value}
      </p>
      {sub ? <p className="text-xs text-base-content/45">{sub}</p> : null}
    </div>
  );
}
