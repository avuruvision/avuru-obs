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
