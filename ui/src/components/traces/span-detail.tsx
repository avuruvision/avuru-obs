"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { formatTime, utcTooltip } from "@/lib/format";
import { spanComponent, spanPeer } from "@/lib/component";
import { spanStatus } from "@/lib/span-status";
import type { Span } from "@/lib/api-types";

// One attribute value cell: wraps long values and reveals a copy affordance on
// hover. `name` is the attribute key, used for the accessible label.
function AttrValue({ name, value }: { name: string; value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard blocked (insecure context) — no-op
    }
  };
  return (
    <dd className="group flex min-w-0 items-start gap-1 font-mono text-xs">
      <span className="min-w-0 break-all">{value}</span>
      <button
        aria-label={`Copy ${name}`}
        onClick={copy}
        className="invisible mt-0.5 shrink-0 text-base-content/40 hover:text-base-content group-hover:visible"
      >
        {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
      </button>
    </dd>
  );
}

function AttrTable({ title, attrs }: { title: string; attrs?: Record<string, string> }) {
  const entries = Object.entries(attrs ?? {});
  if (!entries.length) return null;
  return (
    <div>
      <h4 className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-base-content/50">
        {title}
      </h4>
      {/* Key column is capped at 45% so long keys can never crush the values. */}
      <dl className="grid grid-cols-[minmax(120px,45%)_1fr] gap-x-4 gap-y-0.5">
        {entries.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="break-all font-mono text-xs text-base-content/55">{k}</dt>
            <AttrValue name={k} value={v} />
          </div>
        ))}
      </dl>
    </div>
  );
}

export function SpanDetail({ span }: { span: Span }) {
  const status = spanStatus(span);
  const component = spanComponent(span);
  const peer = spanPeer(span);
  return (
    <div className="flex flex-col gap-3 border-b border-neutral/40 bg-base-100/60 px-4 py-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <Badge
          tone={status.kind === "error" ? "error" : status.kind === "ok" ? "success" : "neutral"}
          title={`OTel status: ${span.statusCode}`}
        >
          {status.label}
        </Badge>
        <Badge tone="neutral">{span.kind}</Badge>
        <Badge tone="neutral">
          <component.Icon className="mr-1 h-3 w-3" />
          {component.name}
        </Badge>
        {peer && <span className="text-base-content/50">→ {peer}</span>}
        <span className="font-mono text-base-content/50">span {span.spanId}</span>
      </div>
      {span.scopeName && (
        <p className="font-mono text-[10px] text-base-content/45">
          {span.scopeName}
          {span.scopeVersion ? `@${span.scopeVersion}` : ""}
        </p>
      )}
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
