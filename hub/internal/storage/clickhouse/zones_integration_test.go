//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TestZoneTrafficIntegration exercises ZoneTraffic against real ClickHouse.
// What can only break here: that sum() over a Float64 column scans into the
// uint64 Bytes field, that the zone attributes resolve out of the Attributes
// map, and that the blank-zone and window filters prune in SQL rather than in
// Go. Tenant isolation is asserted too, because a zone matrix leaking across
// projects would be a tenancy bug wearing a topology costume.
func TestZoneTrafficIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	tr := storage.TimeRange{Start: base, End: base.Add(10 * time.Minute)}
	noRes := map[string]string{} // no avuru.tenant -> Tenant defaults to "default"
	otherRes := map[string]string{"avuru.tenant": "other"}

	pair := func(src, dst string) map[string]string {
		return map[string]string{"src.zone": src, "dst.zone": dst, "k8s.cluster.name": "prod"}
	}

	// a -> b: two cumulative points in-window, summed (the documented
	// approximation) to 4000.
	insertSum(t, store, base.Add(1*time.Minute), interZoneMetric, noRes, pair("eu-west-1a", "eu-west-1b"), 1000)
	insertSum(t, store, base.Add(5*time.Minute), interZoneMetric, noRes, pair("eu-west-1a", "eu-west-1b"), 3000)
	// The reverse crossing is its own pair — direction is not symmetric, and a
	// cloud bill is not either.
	insertSum(t, store, base.Add(2*time.Minute), interZoneMetric, noRes, pair("eu-west-1b", "eu-west-1a"), 500)
	// Must be pruned: an unlabeled node's blank zone, out-of-window, another
	// metric, another tenant.
	insertSum(t, store, base.Add(2*time.Minute), interZoneMetric, noRes, pair("eu-west-1a", ""), 900)
	insertSum(t, store, base.Add(2*time.Minute), interZoneMetric, noRes, pair("", "eu-west-1b"), 700)
	insertSum(t, store, base.Add(30*time.Minute), interZoneMetric, noRes, pair("eu-west-1a", "eu-west-1c"), 800)
	insertSum(t, store, base.Add(2*time.Minute), networkFlowMetric, noRes, pair("eu-west-1a", "eu-west-1c"), 12345)
	insertSum(t, store, base.Add(2*time.Minute), interZoneMetric, otherRes, pair("us-east-1a", "us-east-1b"), 999)

	zones, err := store.ZoneTraffic(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("ZoneTraffic: %v", err)
	}

	got := make(map[string]uint64, len(zones))
	for _, z := range zones {
		got[z.SrcZone+"->"+z.DstZone] = z.Bytes
	}
	if len(got) != 2 {
		t.Fatalf("got %d zone pairs %v, want exactly the two eu-west crossings", len(got), got)
	}
	if got["eu-west-1a->eu-west-1b"] != 4000 {
		t.Errorf("a->b = %d, want 4000 (1000+3000 summed in-window)", got["eu-west-1a->eu-west-1b"])
	}
	if got["eu-west-1b->eu-west-1a"] != 500 {
		t.Errorf("b->a = %d, want 500 — the reverse crossing is a pair of its own", got["eu-west-1b->eu-west-1a"])
	}

	// Ordering contract: heaviest crossing first, so the UI can take the top N.
	if len(zones) > 1 && zones[0].Bytes < zones[1].Bytes {
		t.Errorf("zones not ordered by bytes desc: %v", zones)
	}

	// Tenant isolation: the other project's crossing is invisible here and
	// visible there.
	other, err := store.ZoneTraffic(ctx, storage.ServiceQuery{Tenant: "other", Range: tr})
	if err != nil {
		t.Fatalf("ZoneTraffic(other): %v", err)
	}
	if len(other) != 1 || other[0].SrcZone != "us-east-1a" || other[0].Bytes != 999 {
		t.Fatalf("tenant other = %v, want only its own us-east crossing", other)
	}
}
