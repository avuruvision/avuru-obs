// Renders a stack trace as monospace lines. Frame parsing/deminification is a
// later phase; today the raw trace is shown verbatim, one frame per line.
export function Stacktrace({ trace }: { trace: string }) {
  if (!trace) {
    return (
      <p className="text-sm text-base-content/50">No stack trace was captured for this error.</p>
    );
  }
  const lines = trace.split("\n");
  return (
    <pre className="overflow-x-auto rounded-lg border border-neutral bg-base-200 p-3 text-xs leading-relaxed">
      {lines.map((line, i) => (
        <div key={i} className="whitespace-pre text-base-content/80">
          {line || " "}
        </div>
      ))}
    </pre>
  );
}
