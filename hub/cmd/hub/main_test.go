package main

import (
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/collection"
)

// The applier's fallbacks are a safety property, not a convenience: every path
// that cannot reach a cluster must degrade to the no-op rather than half-build
// a client that writes to the wrong namespace. The in-cluster success path is
// covered by the collection package's fake-clientset tests; what cannot be
// exercised there is that a compose run or a misconfigured pod never gets a
// real applier at all.
func TestCollectionApplierFallsBackWithoutCluster(t *testing.T) {
	if _, ok := collectionApplier(false).(collection.NoopApplier); !ok {
		t.Error("runtime control off must not build a cluster applier")
	}

	// Enabled but no identity env: the chart injects all three together, so a
	// partial set means the hub is not the pod the chart deployed.
	t.Setenv("AVURUOBS_RELEASE_NAMESPACE", "avuruobs")
	t.Setenv("AVURUOBS_RELEASE_NAME", "")
	t.Setenv("AVURUOBS_COLLECTION_FULLNAME", "avuruobs")
	if _, ok := collectionApplier(true).(collection.NoopApplier); !ok {
		t.Error("incomplete release identity must not build a cluster applier")
	}

	// Full identity but no service-account token (any non-cluster run):
	// rest.InClusterConfig fails and the overlay must still persist.
	t.Setenv("AVURUOBS_RELEASE_NAME", "avuruobs")
	if _, ok := collectionApplier(true).(collection.NoopApplier); !ok {
		t.Error("running outside a cluster must not build a cluster applier")
	}
}
