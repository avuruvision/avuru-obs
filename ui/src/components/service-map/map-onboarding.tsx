import { Cable, Network, Server } from "lucide-react";
import { Card } from "@/components/ui/card";
import { buttonVariants } from "@/components/ui/button";
import { DOCS_BASE } from "@/components/layout/nav-config";

export function MapOnboarding() {
  return (
    <section className="mx-auto max-w-4xl py-8" aria-label="First connection">
      <Network className="h-9 w-9 text-primary" aria-hidden />
      <p className="mt-6 text-xs uppercase tracking-widest text-base-content/65">No services yet</p>
      <h1 className="mt-3 text-4xl font-medium leading-tight">Your system has a shape.<br />Let’s discover it.</h1>
      <p className="mt-4 max-w-xl text-sm leading-relaxed text-base-content/75">No services were observed in this project and time range. Check the global time range, or make your first connection below.</p>
      <div className="mt-8 grid gap-5 sm:grid-cols-2">
        <Card className="p-6">
          <Server className="h-6 w-6 text-primary" aria-hidden />
          <h2 className="mt-5 text-xl font-medium">Discover with eBPF</h2>
          <p className="mt-3 text-sm leading-relaxed text-base-content/75">Observe supported Kubernetes workloads without application changes. Verify Linux kernel and protocol support, install the Helm chart, then generate traffic.</p>
          <a href={`${DOCS_BASE}/getting-started/30-seconds`} target="_blank" rel="noopener noreferrer" className={`${buttonVariants({variant:"primary"})} mt-6`}>Installation guide ↗</a>
        </Card>
        <Card className="p-6">
          <Cable className="h-6 w-6 text-primary" aria-hidden />
          <h2 className="mt-5 text-xl font-medium">Bring OpenTelemetry</h2>
          <p className="mt-3 text-sm leading-relaxed text-base-content/75">Connect an existing SDK or collector to the gateway. Check the project ingest key and service identity, send a request, then follow its trace.</p>
          <a href={`${DOCS_BASE}/getting-started/first-trace`} target="_blank" rel="noopener noreferrer" className={`${buttonVariants()} mt-6`}>Send your first trace ↗</a>
        </Card>
      </div>
      <p className="mt-6 text-xs text-base-content/65">Traces draw the application relationships. Network flows can add connections that traces do not observe.</p>
    </section>
  );
}
