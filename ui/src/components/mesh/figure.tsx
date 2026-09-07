// One labelled reading in a <dl> grid: the proxy page's figures and the
// Security tab's summary share it so a number reads the same on both.
//
// `hint` goes on the title (hover); `note` is a visible second line for the
// part of the reading that must not hide behind a hover — "2 waiting" under a
// carried-workloads count is the finding, not a footnote.
export function Figure({
  label,
  value,
  tone,
  hint,
  note,
}: {
  label: string;
  value: string;
  tone?: "warning" | "error";
  hint?: string;
  note?: string;
}) {
  const color = tone === "warning" ? "text-warning" : tone === "error" ? "text-error" : "";
  return (
    <div title={hint}>
      <dt className="text-xs text-base-content/55">{label}</dt>
      <dd className={`mt-0.5 text-lg tabular-nums ${color}`}>{value}</dd>
      {note && <dd className={`text-xs ${color || "text-base-content/55"}`}>{note}</dd>}
    </div>
  );
}
