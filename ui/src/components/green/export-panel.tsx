"use client";

import { useState } from "react";
import { Download, FileJson, FileSpreadsheet } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { apiDownload } from "@/lib/api";
import { useProject } from "@/lib/project-context";
import type { TimeParams } from "@/lib/query-keys";

type Format = "csv" | "json";

// CSRD export: the per-service numbers plus the methodology block the report
// endpoint embeds, downloaded straight to disk (apiDownload → blob → <a>). The
// period matches the dashboard's current time range.
export function ExportPanel({ time }: { time: TimeParams }) {
  const { project } = useProject();
  const [busy, setBusy] = useState<Format | null>(null);
  const [error, setError] = useState<string | null>(null);

  const download = async (format: Format) => {
    setBusy(format);
    setError(null);
    try {
      await apiDownload(
        "/api/v1/green/report",
        `green-report.${format}`,
        { ...time, format },
        { project },
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "Export failed");
    } finally {
      setBusy(null);
    }
  };

  return (
    <Card className="flex flex-col gap-3 p-4">
      <div className="flex items-center gap-2">
        <Download className="h-4 w-4 text-primary" aria-hidden />
        <h3 className="text-sm font-semibold">CSRD export</h3>
      </div>
      <p className="text-xs text-base-content/55">
        Per-service energy and carbon for the selected window, with the
        methodology block — formula, factor provenance, coverage, and metric
        names — an auditor needs to reproduce the numbers.
      </p>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="secondary"
          size="sm"
          disabled={busy !== null}
          onClick={() => download("csv")}
        >
          <FileSpreadsheet className="h-3.5 w-3.5" aria-hidden />
          {busy === "csv" ? "Preparing…" : "Download CSV"}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy !== null}
          onClick={() => download("json")}
        >
          <FileJson className="h-3.5 w-3.5" aria-hidden />
          {busy === "json" ? "Preparing…" : "Download JSON"}
        </Button>
      </div>
      {error && <p className="text-xs text-error">{error}</p>}
    </Card>
  );
}
