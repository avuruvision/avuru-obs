"use client";

// What the map's channels mean. Dense single row: the map is the content, and
// a legend that takes vertical space costs the thing it explains.
//
// `health` false = the service-health module is off, so there are no status
// rings to explain — the map falls back to marking error presence.
export function MapLegend({
  health,
  carbon,
  infra,
}: {
  health: boolean;
  carbon: boolean;
  infra: boolean;
}) {
  return (
    <div
      data-testid="map-legend"
      className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-base-content/55"
    >
      {health ? (
        <span className="flex items-center gap-3">
          <span className="text-base-content/45">ring:</span>
          <Swatch className="border-success" label="Healthy" />
          <Swatch className="border-warning" label="Degraded" />
          <Swatch className="border-error" label="Down" />
          <Swatch className="border-base-content/30" label="Idle" />
        </span>
      ) : (
        <span>ring: red = errors in window</span>
      )}
      <span>size = rate</span>
      <span>width = calls</span>
      <span className="text-warning/80">amber dashed = network health</span>
      <span className="text-error/80">red = errors</span>
      <span>dotted = observed connection, no traced calls</span>
      {infra && <span>diamond = mesh or gateway</span>}
      {carbon && <span className="text-success/80">halo = gCO2e</span>}
      <span className="text-base-content/40">hover a node for its edges</span>
    </div>
  );
}

function Swatch({ className, label }: { className: string; label: string }) {
  return (
    <span className="flex items-center gap-1">
      <span className={`h-2.5 w-2.5 rounded-full border-2 ${className}`} />
      {label}
    </span>
  );
}
