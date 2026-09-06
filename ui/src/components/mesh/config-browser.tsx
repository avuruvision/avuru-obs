"use client";

import { useMemo } from "react";
import { AlertTriangle, ArrowLeft, FileCode, Info } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Select } from "@/components/ui/select";
import { EmptyState } from "@/components/ui/empty-state";
import { CenteredSpinner } from "@/components/ui/spinner";
import { SortableTh, useColumnSort, type SortColumn } from "@/components/ui/sortable";
import { useURLState } from "@/hooks/use-url-state";
import { useMeshConfig } from "@/hooks/use-mesh-data";
import type { MeshConfigObject, MeshFinding } from "@/lib/api-types";

type SortKey = "kind" | "namespace" | "name" | "issues";
type Row = MeshConfigObject & { issues: number };

const KIND: SortColumn<SortKey> = { key: "kind", label: "Kind" };
const NAMESPACE: SortColumn<SortKey> = { key: "namespace", label: "Namespace" };
const NAME: SortColumn<SortKey> = { key: "name", label: "Name" };
const ISSUES: SortColumn<SortKey> = { key: "issues", label: "Issues", numeric: true };

// The mesh's configuration, and whether it holds together.
//
// Every check behind the Issues column is aimed at breakage that produces
// SILENCE — a route whose backend does not exist drops requests without
// emitting a span. That is why this list is worth reading rather than
// measuring: the traffic half of the product is blindest exactly here.
export function ConfigBrowser() {
  const { get, setMany } = useURLState();
  const kind = get("kind") ?? "";
  const namespace = get("cfgns") ?? "";
  const selected = get("object") ?? "";

  const list = useMeshConfig(true, { kind: kind || undefined, namespace: namespace || undefined });
  const objects = useMemo(() => list.data?.objects ?? [], [list.data]);

  const kinds = useMemo(
    () => [...new Set((list.data?.objects ?? []).map((o) => o.kind))].sort(),
    [list.data],
  );
  const namespaces = useMemo(
    () =>
      [...new Set((list.data?.objects ?? []).map((o) => o.namespace).filter(Boolean))].sort() as string[],
    [list.data],
  );

  if (list.isLoading) return <CenteredSpinner />;
  if (list.data && list.data.state !== "ok") {
    return (
      <EmptyState icon={FileCode} title="Cluster configuration not read">
        {list.data.reason}
      </EmptyState>
    );
  }
  if (selected) {
    const [selKind, selNs, selName] = selected.split("/");
    return (
      <ObjectDetail
        kind={selKind}
        namespace={selNs}
        name={selName}
        onBack={() => setMany({ object: undefined })}
      />
    );
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Select
          ariaLabel="Filter by kind"
          className="w-52"
          value={kind}
          onChange={(v) => setMany({ kind: v || undefined })}
          options={[{ value: "", label: "All kinds" }, ...kinds.map((k) => ({ value: k, label: k }))]}
        />
        {namespaces.length > 1 && (
          <Select
            ariaLabel="Filter configuration by namespace"
            className="w-52"
            value={namespace}
            onChange={(v) => setMany({ cfgns: v || undefined })}
            options={[
              { value: "", label: "All namespaces" },
              ...namespaces.map((n) => ({ value: n, label: n })),
            ]}
          />
        )}
      </div>
      {objects.length === 0 ? (
        <EmptyState icon={FileCode} title="No configuration objects match">
          This cluster may run no mesh configuration of these kinds, or the
          filters exclude everything.
        </EmptyState>
      ) : (
        <ObjectTable objects={objects} onSelect={(o) => setMany({ object: `${o.kind}/${o.namespace ?? ""}/${o.name}` })} />
      )}
    </div>
  );
}

function ObjectTable({
  objects,
  onSelect,
}: {
  objects: MeshConfigObject[];
  onSelect: (o: MeshConfigObject) => void;
}) {
  // Worst first: the reason to open this screen is that something is wrong.
  const sort = useColumnSort<SortKey>("issues", false);
  const rows = useMemo(
    () => sort.sortRows<Row>(objects.map((o) => ({ ...o, issues: o.findings?.length ?? 0 }))),
    [objects, sort],
  );

  return (
    <Card className="overflow-hidden">
      <div className="overflow-x-auto">
        <table data-testid="mesh-config" className="table-dense w-full text-sm">
          <thead className="text-xs text-base-content/55">
            <tr className="border-b border-neutral text-left">
              <SortableTh col={KIND} sort={sort} />
              <SortableTh col={NAMESPACE} sort={sort} />
              <SortableTh col={NAME} sort={sort} />
              <SortableTh col={ISSUES} sort={sort} iconFirst />
            </tr>
          </thead>
          <tbody>
            {rows.map((o) => (
              <tr
                key={`${o.kind}/${o.namespace}/${o.name}`}
                onClick={() => onSelect(o)}
                className="cursor-pointer border-b border-neutral/50 last:border-0 hover:bg-base-300/40"
              >
                <td>
                  <Badge>{o.kind}</Badge>
                </td>
                <td className="text-base-content/70">{o.namespace || "—"}</td>
                <td className="font-medium">
                  <button type="button" className="text-left hover:text-primary hover:underline">
                    {o.name}
                  </button>
                </td>
                <td className="text-right tabular-nums">
                  {o.issues === 0 ? (
                    <span className="text-base-content/40">—</span>
                  ) : (
                    <span className={worstSeverity(o.findings) === "error" ? "text-error" : "text-warning"}>
                      {o.issues}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function ObjectDetail({
  kind,
  namespace,
  name,
  onBack,
}: {
  kind: string;
  namespace: string;
  name: string;
  onBack: () => void;
}) {
  const one = useMeshConfig(true, { kind, namespace: namespace || undefined, name });
  const object = one.data?.objects?.[0];

  if (one.isLoading) return <CenteredSpinner />;
  if (!object) {
    return (
      <EmptyState icon={FileCode} title={`${kind} ${name} is no longer in the cluster`}>
        It may have been deleted since this page was linked.
      </EmptyState>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1 text-xs text-base-content/60 hover:text-base-content"
        >
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden />
          All configuration
        </button>
        <Badge>{object.kind}</Badge>
        <h1 className="font-mono text-lg">{object.name}</h1>
        {object.namespace && (
          <span className="text-xs text-base-content/60">{object.namespace}</span>
        )}
      </div>

      {object.findings?.length ? (
        <div data-testid="mesh-findings" className="flex flex-col gap-2">
          {object.findings.map((f, i) => (
            <FindingCard key={`${f.code}-${i}`} finding={f} />
          ))}
        </div>
      ) : (
        <p className="text-xs text-base-content/55">
          Nothing this product checks for is wrong with this object.
        </p>
      )}

      <Card className="overflow-hidden p-0">
        <div className="border-b border-neutral px-4 py-2 text-xs text-base-content/55">
          spec
        </div>
        <pre className="overflow-x-auto p-4 text-xs leading-relaxed">
          {JSON.stringify(object.spec ?? {}, null, 2)}
        </pre>
      </Card>
    </div>
  );
}

function FindingCard({ finding }: { finding: MeshFinding }) {
  const isError = finding.severity === "error";
  return (
    <Card className={`p-3 ${isError ? "border-error/40" : "border-warning/40"}`}>
      <div className="flex items-start gap-2">
        {isError ? (
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-error" aria-hidden />
        ) : (
          <Info className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden />
        )}
        <div className="min-w-0">
          <p className="text-sm">{finding.message}</p>
          {/* The fix, not just the fault. */}
          {finding.hint && (
            <p className="mt-1 text-xs text-base-content/60">{finding.hint}</p>
          )}
          <p className="mt-1 font-mono text-[10px] uppercase tracking-wider text-base-content/35">
            {finding.code}
          </p>
        </div>
      </div>
    </Card>
  );
}

function worstSeverity(findings?: MeshFinding[]): string {
  return findings?.some((f) => f.severity === "error") ? "error" : "warning";
}
