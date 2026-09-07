package clickhouse

import (
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// A composed read is one query: one OR branch per source, each narrowed to
// its services and — when it has needles — to the bodies that contain one
// of each list. The plain service filter stays for a query with no sources.
func TestLogSourceFilterRendersOneBranchPerSource(t *testing.T) {
	q := storage.LogQuery{Sources: []storage.LogSource{
		{Services: []string{"checkout", "checkout.shop"}},
		{Services: []string{"ztunnel"}, BodyAll: [][]string{{"checkout-abc12-x1", "checkout.shop.svc"}}},
		{Services: []string{"global-waypoint"}, BodyAll: [][]string{{"checkout.shop.svc"}, {`namespace="shop"`}}},
	}}
	sql, args := logSourceFilter(q)
	if strings.Count(sql, "ServiceName IN (?)") != 3 || strings.Count(sql, " OR ") != 2 {
		t.Errorf("sql = %s", sql)
	}
	if strings.Count(sql, "multiSearchAny(Body, ?)") != 3 {
		t.Errorf("multiSearchAny count in %s", sql)
	}
	if strings.Contains(sql, "ServiceName = ?") {
		t.Error("the single-service filter rendered beside the sources")
	}
	want := []any{[]string{"checkout", "checkout.shop"},
		[]string{"ztunnel"}, []string{"checkout-abc12-x1", "checkout.shop.svc"},
		[]string{"global-waypoint"}, []string{"checkout.shop.svc"}, []string{`namespace="shop"`}}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if a, ok := args[i].([]string); !ok || strings.Join(a, ",") != strings.Join(want[i].([]string), ",") {
			t.Errorf("args[%d] = %v, want %v", i, args[i], want[i])
		}
	}
}

func TestLogSourceFilterFallsBackToTheServiceFilter(t *testing.T) {
	sql, args := logSourceFilter(storage.LogQuery{Service: "checkout"})
	if !strings.Contains(sql, "ServiceName = ?") || strings.Contains(sql, " OR ") || len(args) != 1 || args[0] != "checkout" {
		t.Errorf("sql = %s args = %v", sql, args)
	}
	if sql, args := logSourceFilter(storage.LogQuery{}); sql != "" || len(args) != 0 {
		t.Errorf("no filter rendered %q %v", sql, args)
	}
}
