// Duration/timestamp formatting: locale rendering with UTC available on
// hover (agent_docs/ui_patterns.md rule 4).

export function formatMs(ms: number): string {
  if (ms < 1) return `${(ms * 1000).toFixed(0)}µs`;
  if (ms < 1000) return `${ms.toFixed(ms < 10 ? 1 : 0)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(2)}s`;
  const m = Math.floor(s / 60);
  return `${m}m${Math.round(s % 60)}s`;
}

export function formatRate(perSec: number): string {
  if (perSec >= 10) return `${perSec.toFixed(0)}/s`;
  if (perSec >= 0.1) return `${perSec.toFixed(1)}/s`;
  return `${(perSec * 60).toFixed(1)}/min`;
}

export function formatPercent(ratio: number): string {
  if (ratio === 0) return "—";
  if (ratio < 0.01) return "<1%";
  return `${(ratio * 100).toFixed(1)}%`;
}

export function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function utcTooltip(iso: string): string {
  return new Date(iso).toISOString();
}

export function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(
    units.length - 1,
    Math.floor(Math.log(bytes) / Math.log(1024)),
  );
  const v = bytes / Math.pow(1024, i);
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

// Energy & carbon (module green). Values are small at the pod scale and large
// at the fleet scale, so each formatter promotes to the next unit rather than
// print a wall of digits — the same "readable at any magnitude" spirit as
// formatBytes/formatMs.

export function formatWh(wh: number): string {
  if (wh <= 0) return "0 Wh";
  if (wh >= 1_000_000) return `${(wh / 1_000_000).toFixed(2)} MWh`;
  if (wh >= 1000) return `${(wh / 1000).toFixed(2)} kWh`;
  if (wh >= 10) return `${wh.toFixed(1)} Wh`;
  return `${wh.toFixed(2)} Wh`;
}

export function formatGco2e(g: number): string {
  if (g <= 0) return "0 g";
  if (g >= 1_000_000) return `${(g / 1_000_000).toFixed(2)} t`;
  if (g >= 1000) return `${(g / 1000).toFixed(2)} kg`;
  if (g >= 10) return `${g.toFixed(1)} g`;
  return `${g.toFixed(2)} g`;
}

export function formatKgCo2e(kg: number): string {
  if (kg <= 0) return "0 kg";
  if (kg >= 1000) return `${(kg / 1000).toFixed(2)} t`;
  if (kg >= 10) return `${kg.toFixed(1)} kg`;
  return `${kg.toFixed(2)} kg`;
}

// mgCO2e per request — the per-request carbon intensity. 0/absent renders as a
// dash (a service that saw no requests has no intensity).
export function formatMgPerReq(mg: number | undefined): string {
  if (!mg || mg <= 0) return "—";
  if (mg >= 1000) return `${(mg / 1000).toFixed(2)} g/req`;
  if (mg >= 10) return `${mg.toFixed(1)} mg/req`;
  return `${mg.toFixed(2)} mg/req`;
}

export function formatAgo(iso: string): string {
  const sec = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${Math.round(sec)}s ago`;
  if (sec < 3600) return `${Math.round(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.round(sec / 3600)}h ago`;
  return `${Math.round(sec / 86400)}d ago`;
}
