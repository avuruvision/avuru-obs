package api

import (
	"context"
	"log/slog"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// stampEdgeSecurity overlays the data plane's per-edge security onto the
// map's edges. Best-effort, like the namespaces: a failed read costs the
// marker, never the map.
func (a *API) stampEdgeSecurity(
	ctx context.Context, store storage.Store, q storage.ServiceQuery,
	edges []serviceEdgeDTO, namespaces map[string]string,
) {
	sec, err := store.MeshSecurity(ctx, q)
	if err != nil {
		slog.Warn("service map: data-plane security read failed; edges carry no mTLS share", "error", err)
		return
	}
	if !sec.Available {
		return
	}
	applyEdgeSecurity(edges, sec.Edges, namespaces)
}

type meshEdgePair struct{ src, dst nsWorkloadKey }

// applyEdgeSecurity joins the map's (source, target) service names to the
// proxies' (namespace, workload) pairs through workloadKey on both ends.
//
// Collapsed edges stay unmarked, on purpose (v1): a dependency recovered
// across a proxy has no single reporting side — the caller's hop to the
// proxy and the proxy's hop to the callee are two edges with two policies,
// and stamping either on the recovered pair would attribute one hop's
// security to the whole path.
func applyEdgeSecurity(edges []serviceEdgeDTO, secured []storage.MeshEdgeSecurity, namespaces map[string]string) {
	byPair := make(map[meshEdgePair]storage.MeshSecurityCounts, len(secured))
	for _, e := range secured {
		byPair[meshEdgePair{
			nsWorkloadKey{e.SourceNamespace, e.Source},
			nsWorkloadKey{e.TargetNamespace, e.Target},
		}] = e.Counts
	}
	for i := range edges {
		e := &edges[i]
		if e.ViaTransport != nil {
			continue
		}
		src, dst := nsWorkloadKeyOf(e.Source, namespaces), nsWorkloadKeyOf(e.Target, namespaces)
		if src.ns == "" || dst.ns == "" {
			continue
		}
		counts, ok := byPair[meshEdgePair{src, dst}]
		if !ok {
			continue
		}
		o := observe(storage.MeshWorkloadSecurity{Counts: counts})
		e.MTLSShare = o.mtlsShare()
		e.PlaintextCalls = o.plaintext
	}
}
