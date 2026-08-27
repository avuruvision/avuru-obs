package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func decodeBreakdown(t *testing.T, fake *storagetest.Fake, path string) breakdownResponse {
	t.Helper()
	rec := get(t, newMux(fake), path)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp breakdownResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// The grouping vocabulary is a closed set. An unknown name must be a 400 that
// names the alternatives, never a 500 from SQL and never a query built out of
// caller text.
func TestBreakdownRejectsUnknownGrouping(t *testing.T) {
	for _, groupBy := range []string{
		"ServiceName",              // a column name, not a public dimension
		"service; DROP TABLE otel", // the injection this closed set exists to stop
		"attribute:",               // parameterised, with no key
		"nonsense:x",               // unknown parameterised family
	} {
		rec := get(t, newMux(&storagetest.Fake{}), "/api/v1/traces/breakdown?groupBy="+url.QueryEscape(groupBy))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("groupBy=%q: status %d, want 400 (body %s)", groupBy, rec.Code, rec.Body.String())
		}
	}
}

func TestBreakdownParsesDimensionsAndScope(t *testing.T) {
	tests := []struct {
		query    string
		wantDim  storage.BreakdownDimension
		wantKey  string
		wantScop storage.BreakdownScope
	}{
		{"", storage.BreakdownService, "", storage.ScopeEntry},
		{"groupBy=operation", storage.BreakdownOperation, "", storage.ScopeEntry},
		{"groupBy=status&scope=root", storage.BreakdownStatus, "", storage.ScopeRoot},
		{"groupBy=kind&scope=all", storage.BreakdownKind, "", storage.ScopeAll},
		{"groupBy=attribute:http.route", storage.BreakdownAttribute, "http.route", storage.ScopeEntry},
		{"groupBy=resource:k8s.namespace.name", storage.BreakdownResource, "k8s.namespace.name", storage.ScopeEntry},
	}
	for _, tc := range tests {
		fake := &storagetest.Fake{}
		path := "/api/v1/traces/breakdown"
		if tc.query != "" {
			path += "?" + tc.query
		}
		decodeBreakdown(t, fake, path)
		got := fake.LastBreakdownQuery
		if got.GroupBy != tc.wantDim || got.Key != tc.wantKey || got.Scope != tc.wantScop {
			t.Errorf("%q -> dim=%q key=%q scope=%q; want %q/%q/%q",
				tc.query, got.GroupBy, got.Key, got.Scope, tc.wantDim, tc.wantKey, tc.wantScop)
		}
	}
}

// A breakdown must carry the same filters as the trace list it sits above, or
// the chart and the rows beneath it describe different traffic.
func TestBreakdownPassesTraceFilters(t *testing.T) {
	fake := &storagetest.Fake{}
	decodeBreakdown(t, fake,
		"/api/v1/traces/breakdown?service=cart&operation=GET+%2Fitems&status=error"+
			"&minDurationMs=10&maxDurationMs=250&tags=http.method%3DGET&includeAux=true&limit=5")

	q := fake.LastBreakdownQuery
	if q.Service != "cart" || q.Operation != "GET /items" || q.Status != "error" {
		t.Errorf("filters lost: %+v", q)
	}
	if q.MinDuration != 10*time.Millisecond || q.MaxDuration != 250*time.Millisecond {
		t.Errorf("duration bounds wrong: min=%v max=%v", q.MinDuration, q.MaxDuration)
	}
	if q.Tags["http.method"] != "GET" {
		t.Errorf("tags lost: %+v", q.Tags)
	}
	if q.ExcludeAux {
		t.Error("includeAux=true must switch the aux exclusion off")
	}
	if q.Limit != 5 {
		t.Errorf("limit = %d, want 5", q.Limit)
	}
}

// Auxiliary traffic is excluded unless asked for — the same default as every
// other trace read, so the charts agree with the tables.
func TestBreakdownExcludesAuxByDefault(t *testing.T) {
	fake := &storagetest.Fake{}
	decodeBreakdown(t, fake, "/api/v1/traces/breakdown")
	if !fake.LastBreakdownQuery.ExcludeAux {
		t.Error("aux traffic must be excluded by default")
	}
}

// The tail matters: a part-of-whole chart drawn from the returned groups alone
// would redraw a top-N as the whole estate. Everything the groups do not
// account for has to come back as its own bucket.
func TestBreakdownReportsTheTail(t *testing.T) {
	fake := &storagetest.Fake{
		Breakdown: storage.Breakdown{
			Groups: []storage.BreakdownGroup{
				{Key: "cart", Count: 60, ErrorCount: 6, DurationSum: 6 * time.Second, P95: 30 * time.Millisecond},
				{Key: "checkout", Count: 30, ErrorCount: 3, DurationSum: 3 * time.Second},
			},
			Total:      storage.BreakdownGroup{Count: 100, ErrorCount: 12, RefusedCount: 4, DurationSum: 12 * time.Second},
			GroupCount: 17,
		},
	}
	resp := decodeBreakdown(t, fake, "/api/v1/traces/breakdown?limit=2")

	if resp.Other == nil {
		t.Fatal("no other bucket for a truncated breakdown")
	}
	if resp.Other.Count != 10 {
		t.Errorf("other count = %d, want 10 (100 total - 90 shown)", resp.Other.Count)
	}
	if resp.Other.ErrorCount != 3 {
		t.Errorf("other errors = %d, want 3", resp.Other.ErrorCount)
	}
	if resp.Other.DurationMsSum != 3000 {
		t.Errorf("other duration = %v ms, want 3000", resp.Other.DurationMsSum)
	}
	// Quantiles cannot be recovered by subtraction, so the tail must claim none.
	if resp.Other.P50Ms != 0 || resp.Other.P95Ms != 0 || resp.Other.P99Ms != 0 {
		t.Errorf("other bucket must carry no quantiles: %+v", resp.Other)
	}
	if resp.GroupCount != 17 {
		t.Errorf("groupCount = %d, want 17", resp.GroupCount)
	}
	if resp.Total.Count != 100 {
		t.Errorf("total = %d, want 100", resp.Total.Count)
	}
}

// With nothing past the limit there is no tail, and inventing an empty slice
// would put a 0% wedge in every chart.
func TestBreakdownOmitsEmptyTail(t *testing.T) {
	fake := &storagetest.Fake{
		Breakdown: storage.Breakdown{
			Groups: []storage.BreakdownGroup{{Key: "cart", Count: 40, DurationSum: time.Second}},
			Total:  storage.BreakdownGroup{Count: 40, DurationSum: time.Second},
		},
	}
	resp := decodeBreakdown(t, fake, "/api/v1/traces/breakdown")
	if resp.Other != nil {
		t.Errorf("unexpected other bucket: %+v", resp.Other)
	}
}

// A span with no value for the grouping attribute is its own answer — "how much
// of my traffic is unlabelled" is what a tagging rollout is asking — so the
// empty key must survive the round trip rather than being dropped.
func TestBreakdownKeepsTheEmptyKey(t *testing.T) {
	fake := &storagetest.Fake{
		Breakdown: storage.Breakdown{
			Groups: []storage.BreakdownGroup{{Key: "", Count: 25}, {Key: "payments", Count: 25}},
			Total:  storage.BreakdownGroup{Count: 50},
		},
	}
	resp := decodeBreakdown(t, fake, "/api/v1/traces/breakdown?groupBy=resource:k8s.namespace.name")
	if len(resp.Groups) != 2 || resp.Groups[0].Key != "" {
		t.Fatalf("empty key dropped: %+v", resp.Groups)
	}
}
