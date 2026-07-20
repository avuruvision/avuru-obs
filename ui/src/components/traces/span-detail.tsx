"use client";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Stacktrace } from "@/components/errors/stacktrace";
import { useURLState } from "@/hooks/use-url-state";
import { formatTime, utcTooltip } from "@/lib/format";
import { cn } from "@/lib/cn";
import { serviceColor } from "@/lib/trace";
import { spanComponent, spanPeer } from "@/lib/component";
import { spanStatus } from "@/lib/span-status";
import type { Span, SpanEvent } from "@/lib/api-types";
import { CLEAR_WORKSPACE_PARAMS } from "./workspace-params";

// isErrorAttr flags application-level error signals so the panel highlights them
// even when the OTel span status is Ok/Unset (many SDKs record error.*/exception.*
// attributes without setting the span status). This is display-only — it does
// NOT reclassify the span (span-status.ts stays in sync with the backend's error
// counting, so the badge and RED error counts never disagree).
function isErrorAttr(key: string, value: string): boolean {
  const k = key.toLowerCase();
  if (k === "status" && value.toLowerCase() === "error") return true;
  if (k === "otel.status_code" && value.toUpperCase() === "ERROR") return true;
  return k.startsWith("error.") || k.startsWith("exception.");
}

// One attribute value cell: wraps long values, reveals a copy button on hover,
// and renders in the error tone when it's an error signal.
function AttrValue({ name, value, error }: { name: string; value: string; error?: boolean }) {
  return (
    <dd className="group flex min-w-0 items-start gap-1 font-mono text-xs">
      <span className={cn("min-w-0 break-all", error && "text-error")}>{value}</span>
      <CopyButton
        value={value}
        ariaLabel={`Copy ${name}`}
        iconClass="h-3 w-3"
        className="invisible mt-0.5 group-hover:visible"
      />
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
        {entries.map(([k, v]) => {
          const err = isErrorAttr(k, v);
          return (
            <div key={k} className="contents">
              <dt className={cn("break-all font-mono text-xs", err ? "text-error/80" : "text-base-content/55")}>
                {k}
              </dt>
              <AttrValue name={k} value={v} error={err} />
            </div>
          );
        })}
      </dl>
    </div>
  );
}

// SpanEventItem renders one span event. An exception event (exception.type /
// message / stacktrace) is formatted: the type+message in the error tone, the
// stack trace in a monospace block with a copy button, and any remaining
// attributes as a small table — instead of one flattened `k=v k=v` line.
function SpanEventItem({ ev }: { ev: SpanEvent }) {
  const attrs = ev.attributes ?? {};
  const excType = attrs["exception.type"];
  const excMessage = attrs["exception.message"];
  const stacktrace = attrs["exception.stacktrace"];
  const rest = Object.entries(attrs).filter(([k]) => !k.startsWith("exception."));

  return (
    <div className="mb-2">
      <div className="flex items-center gap-2">
        <span className="font-mono text-xs" title={utcTooltip(ev.time)}>
          {formatTime(ev.time)}
        </span>
        <span className="text-xs font-medium">{ev.name}</span>
      </div>
      {(excType || excMessage) && (
        <p className="mt-0.5 break-all font-mono text-xs text-error">
          {excType}
          {excType && excMessage ? ": " : ""}
          {excMessage}
        </p>
      )}
      {stacktrace && (
        <div className="group relative mt-1">
          <CopyButton
            value={stacktrace}
            ariaLabel="Copy stack trace"
            className="invisible absolute right-2 top-2 z-10 rounded border border-neutral bg-base-100 p-1 group-hover:visible"
          />
          <Stacktrace trace={stacktrace} />
        </div>
      )}
      {rest.length > 0 && (
        <dl className="mt-1 grid grid-cols-[minmax(120px,45%)_1fr] gap-x-4 gap-y-0.5">
          {rest.map(([k, v]) => {
            const err = isErrorAttr(k, v);
            return (
              <div key={k} className="contents">
                <dt className={cn("break-all font-mono text-xs", err ? "text-error/80" : "text-base-content/55")}>
                  {k}
                </dt>
                <AttrValue name={k} value={v} error={err} />
              </div>
            );
          })}
        </dl>
      )}
    </div>
  );
}

export function SpanDetail({ span }: { span: Span }) {
  const { setMany } = useURLState();
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
        <span className="inline-flex items-center gap-1 font-mono text-base-content/50">
          span {span.spanId}
          <CopyButton value={span.spanId} ariaLabel="Copy span id" iconClass="h-3 w-3" />
        </span>
        <CopyButton
          value={JSON.stringify(span, null, 2)}
          label="JSON"
          ariaLabel="Copy span JSON"
          iconClass="h-3 w-3"
          className="ml-auto"
        />
      </div>
      {/* Service perspective: jump from this span to the service's traces
          (participant filter) or to every request hitting the same endpoint. */}
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        <span className="inline-flex items-center gap-1.5 font-medium">
          <span
            className="h-2.5 w-2.5 rounded-sm"
            style={{ backgroundColor: serviceColor(span.service) }}
            aria-hidden
          />
          {span.service}
        </span>
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            setMany({ ...CLEAR_WORKSPACE_PARAMS, service: span.service, tab: "traces" })
          }
        >
          Service traces
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            setMany({
              ...CLEAR_WORKSPACE_PARAMS,
              service: span.service,
              operation: span.operation,
              tab: "traces",
            })
          }
        >
          Traces of this operation
        </Button>
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
            <SpanEventItem key={i} ev={ev} />
          ))}
        </div>
      )}
    </div>
  );
}
