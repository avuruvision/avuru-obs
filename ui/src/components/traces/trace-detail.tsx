"use client";

import { X } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { formatMs, formatTime, utcTooltip } from "@/lib/format";
import { useTrace } from "@/hooks/use-traces-data";
import { Waterfall } from "./waterfall";

// Selected via ?trace=<id> so the URL stays pasteable (static export: no
// dynamic routes — agent_docs/ui_patterns.md).
export function TraceDetail({
  traceId,
  onClose,
}: {
  traceId: string;
  onClose: () => void;
}) {
  const { data, isLoading, error } = useTrace(traceId);

  return (
    <Card>
      <CardHeader>
        <div className="flex min-w-0 items-center gap-2">
          <CardTitle className="truncate font-mono">{traceId}</CardTitle>
          {data && (
            <>
              <Badge tone="primary">{data.spans.length} spans</Badge>
              <Badge tone="neutral">{formatMs(data.durationMs)}</Badge>
              <span
                className="text-xs text-base-content/50"
                title={utcTooltip(data.startTime)}
              >
                {formatTime(data.startTime)}
              </span>
            </>
          )}
        </div>
        <Button variant="ghost" size="icon" aria-label="Close trace" onClick={onClose}>
          <X className="h-4 w-4" />
        </Button>
      </CardHeader>
      <div className="px-4 pb-4">
        {isLoading && <CenteredSpinner />}
        {error && (
          <p className="p-4 text-sm text-error">
            Trace not found — it may have aged out of retention.
          </p>
        )}
        {data && <Waterfall trace={data} />}
      </div>
    </Card>
  );
}
