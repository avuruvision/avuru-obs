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

func TestLogSourceFilterCanExplicitlyMatchNothing(t *testing.T) {
	sql, args := logSourceFilter(storage.LogQuery{MatchNone: true})
	if sql != " AND 0" || len(args) != 0 {
		t.Errorf("match-none filter = %q %v", sql, args)
	}
}

func TestLogSourceFilterSeveralServicesAndCategory(t *testing.T) {
	q := storage.LogQuery{
		Sources: []storage.LogSource{
			{Category: "application", Services: []string{"checkout", "inventory"}},
			{Category: "ztunnel", Services: []string{"ztunnel"}, BodyAll: [][]string{{"checkout-abc", "inventory-def"}}},
		},
		SourceCategories: []string{"application", "ztunnel"},
	}
	sql, args := logSourceFilter(q)
	if strings.Count(sql, "ServiceName IN (?)") != 2 || !strings.Contains(sql, " OR ") {
		t.Errorf("sql = %s", sql)
	}
	if len(args) != 3 {
		t.Errorf("args = %#v", args)
	}
}

func TestLogCategoryFilterWithoutSubjects(t *testing.T) {
	sql, _ := logSourceFilter(storage.LogQuery{SourceCategories: []string{"ztunnel", "other"}})
	if !strings.Contains(sql, "startsWith(ServiceName, 'ztunnel-')") || !strings.Contains(sql, "ServiceName = ''") {
		t.Errorf("sql = %s", sql)
	}
}
