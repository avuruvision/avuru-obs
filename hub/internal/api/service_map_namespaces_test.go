package api

import (
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The map's boundaries come from the same namespace resolution the health
// board's auto-grouping uses, so the two cannot disagree about where a service
// lives. A service that declares neither namespace keeps an empty one — drawn
// outside every boundary, rather than collected into an invented bucket.
func TestStampServiceNamespaces(t *testing.T) {
	services := []serviceDTO{{Name: "web"}, {Name: "cart"}, {Name: "batch"}}
	stampServiceNamespaces([]storage.ServiceLabel{
		{Service: "web", K8sNamespace: "storefront", ServiceNamespace: "ignored"},
		{Service: "cart", ServiceNamespace: "storefront"},
	}, services)

	for i, want := range []string{"storefront", "storefront", ""} {
		if services[i].Namespace != want {
			t.Errorf("namespace(%s) = %q, want %q", services[i].Name, services[i].Namespace, want)
		}
	}
}
