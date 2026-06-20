"use client";

import { Badge } from "@/components/ui/badge";
import { formatTime, utcTooltip } from "@/lib/format";
import type { Span } from "@/lib/api-types";

function AttrTable({ title, attrs }: { title: string; attrs?: Record<string, string> }) {
  const entries = Object.entries(attrs ?? {});
  if (!entries.length) return null;
  return (
    <div>
      <h4 className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-base-content/50">
        {title}
      </h4>
      <dl className="grid grid-cols-[minmax(140px,auto)_1fr] gap-x-4 gap-y-0.5">
        {entries.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="truncate font-mono text-xs text-base-content/55">{k}</dt>
            <dd className="break-all font-mono text-xs">{v}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

export function SpanDetail({ span }: { span: Span }) {
  return (
    <div className="flex flex-col gap-3 border-b border-neutral/40 bg-base-100/60 px-9 py-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <Badge tone={span.statusCode === "Error" ? "error" : "neutral"}>
          {span.statusCode}
        </Badge>
        <Badge tone="neutral">{span.kind}</Badge>
        <span className="font-mono text-base-content/50">span {span.spanId}</span>
      </div>
      {span.statusMessage && (
        <p className="font-mono text-xs text-error">{span.statusMessage}</p>
      )}
      <AttrTable title="Attributes" attrs={span.attributes} />
      <AttrTable title="Resource" attrs={span.resourceAttributes} />
      {span.events && span.events.length > 0 && (
        <div>
          <h4 className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-base-content/50">
            Events
          </h4>
          {span.events.map((ev, i) => (
            <div key={i} className="mb-1">
              <span className="font-mono text-xs" title={utcTooltip(ev.time)}>
                {formatTime(ev.time)}
              </span>{" "}
              <span className="text-xs font-medium">{ev.name}</span>
              {ev.attributes && Object.keys(ev.attributes).length > 0 && (
                <span className="ml-2 font-mono text-xs text-base-content/55">
                  {Object.entries(ev.attributes)
                    .map(([k, v]) => `${k}=${v}`)
                    .join(" ")}
                </span>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
