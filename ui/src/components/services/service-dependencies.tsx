"use client";

import { useMemo } from "react";
import { ArrowDownLeft, ArrowUpRight, Network, TableProperties } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useURLState } from "@/hooks/use-url-state";
import { splitNeighbourhood } from "@/lib/neighbourhood-layout";
import { DependencyTable } from "./dependency-table";
import { ServiceNeighbourhood } from "./service-neighbourhood";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";

type DepsView = "diagram" | "table";

// A service's neighbourhood, derived from the map's own edge set so the two
// screens cannot disagree about what depends on what.
//
// The caller passes edges that have been through splitInfrastructure. That
// matters, and it is not something this component can assume: the hub applies
// the hop collapse and the transport classification to the DATA, but choosing
// between the recovered dependency and the hops it was recovered from is a VIEW
// decision, and it is the caller that makes it.
//
// Two ways to read the same edges. The diagram answers "what shape is this" —
// which caller carries the traffic, which dependency is going red, which hop
// only exists because the hub walked a trace across a proxy. The tables answer
// "exactly which, and exactly how much", and stay the complete list when the
// diagram has more neighbours than it can draw.
export function ServiceDependencies({
  service,
  services,
  edges,
  windowMs,
  health,
  healthEnabled,
  onSelect,
}: {
  service: string;
  services: ServiceStats[];
  edges: ServiceEdge[];
  windowMs: number;
  health: Map<string, ServiceHealth>;
  healthEnabled: boolean;
  onSelect: (service: string) => void;
}) {
  const { get, setMany } = useURLState();
  const view: DepsView = get("deps") === "table" ? "table" : "diagram";

  const { upstream, downstream } = useMemo(
    () => splitNeighbourhood(service, edges),
    [service, edges],
  );

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold text-primary">Neighbourhood</h2>
        <div className="flex items-center gap-2">
          <span className="text-xs text-base-content/50">
            {upstream.length} in · {downstream.length} out
          </span>
          {/* The choice is URL state like everything else on this screen, and
              the DIAGRAM is the default — so `deps` carries the opt-out and a
              plain link to a service still arrives on the picture. */}
          <div className="flex gap-0.5 rounded-lg border border-neutral p-0.5">
            <ViewButton
              label="Diagram"
              icon={Network}
              active={view === "diagram"}
              onClick={() => setMany({ deps: undefined })}
            />
            <ViewButton
              label="Table"
              icon={TableProperties}
              active={view === "table"}
              onClick={() => setMany({ deps: "table" })}
            />
          </div>
        </div>
      </div>

      {view === "diagram" ? (
        <ServiceNeighbourhood
          service={service}
          services={services}
          upstream={upstream}
          downstream={downstream}
          windowMs={windowMs}
          health={health}
          healthEnabled={healthEnabled}
          onSelect={onSelect}
        />
      ) : (
        <div className="flex flex-col gap-3 lg:flex-row">
          <DependencyTable
            title="Called by"
            icon={ArrowDownLeft}
            edges={upstream}
            peerOf={(e) => e.source}
            windowMs={windowMs}
            onSelect={onSelect}
            emptyHint="Nothing observed calling this service in this window. It may be an entry point, or its callers may not be instrumented."
          />
          <DependencyTable
            title="Depends on"
            icon={ArrowUpRight}
            edges={downstream}
            peerOf={(e) => e.target}
            windowMs={windowMs}
            onSelect={onSelect}
            emptyHint="No outgoing calls observed in this window."
          />
        </div>
      )}
    </div>
  );
}

function ViewButton({
  label,
  icon: Icon,
  active,
  onClick,
}: {
  label: string;
  icon: typeof Network;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      variant={active ? "secondary" : "ghost"}
      size="sm"
      aria-pressed={active}
      onClick={onClick}
    >
      <Icon className="h-3.5 w-3.5" aria-hidden />
      {label}
    </Button>
  );
}
