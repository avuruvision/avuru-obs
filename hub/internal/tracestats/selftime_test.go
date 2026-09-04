package tracestats

import (
	"testing"
	"time"
)

func span(id, parent, service string, d time.Duration) storageSpan {
	return storageSpan{SpanID: id, ParentSpanID: parent, Service: service, Duration: d}
}

func TestSelfTimeSubtractsDirectChildren(t *testing.T) {
	got := SelfTimeByService(spans(
		span("root", "", "frontend", 100*time.Millisecond),
		span("child", "root", "payments", 60*time.Millisecond),
	))
	if len(got) != 2 {
		t.Fatalf("want two services, got %v", got)
	}
	// payments has no children, so all 60ms is its own; frontend kept 40ms.
	byName := map[string]ServiceSelfTime{}
	for _, r := range got {
		byName[r.Service] = r
	}
	if byName["frontend"].SelfTime != 40*time.Millisecond {
		t.Errorf("frontend self = %v, want 40ms", byName["frontend"].SelfTime)
	}
	if byName["payments"].SelfTime != 60*time.Millisecond {
		t.Errorf("payments self = %v, want 60ms", byName["payments"].SelfTime)
	}
	// Biggest first.
	if got[0].Service != "payments" {
		t.Errorf("want the biggest contributor first, got %v", got[0].Service)
	}
}

// Concurrent children can outlast their parent's own clock. The result is zero,
// never a negative that would corrupt the rollup.
func TestSelfTimeNeverGoesNegative(t *testing.T) {
	got := SelfTimeByService(spans(
		span("root", "", "frontend", 50*time.Millisecond),
		span("a", "root", "svc-a", 40*time.Millisecond),
		span("b", "root", "svc-b", 40*time.Millisecond),
	))
	for _, r := range got {
		if r.SelfTime < 0 {
			t.Fatalf("%s self = %v, want >= 0", r.Service, r.SelfTime)
		}
	}
}

func TestEffectiveStatus(t *testing.T) {
	cases := []struct {
		name   string
		status string
		kind   string
		attrs  map[string]string
		want   Status
	}{
		{"explicit error wins", "Error", "Server", nil, StatusError},
		{"explicit ok is final even with a 500", "Ok", "Server",
			map[string]string{"http.response.status_code": "500"}, StatusOK},
		// The defect this package fixes: the MCP tool tested only the raw
		// status, so an auto-instrumented 5xx counted as a success.
		{"unset with a 5xx is an error", "Unset", "Server",
			map[string]string{"http.response.status_code": "503"}, StatusError},
		{"client 4xx is an error", "Unset", "Client",
			map[string]string{"http.response.status_code": "404"}, StatusError},
		{"server 4xx is refused, not an error", "Unset", "Server",
			map[string]string{"http.response.status_code": "404"}, StatusRefused},
		{"3xx is never an error", "Unset", "Server",
			map[string]string{"http.response.status_code": "307"}, StatusOK},
		{"no http status at all", "Unset", "Internal", nil, StatusOK},
		// greatest() of both keys, exactly as the SQL resolves the duality.
		{"pre-1.21 key still counts", "Unset", "Server",
			map[string]string{"http.status_code": "500"}, StatusError},
		{"the greater of the two keys wins", "Unset", "Server",
			map[string]string{"http.status_code": "200", "http.response.status_code": "500"}, StatusError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp := storageSpan{StatusCode: c.status, Kind: c.kind, Attributes: c.attrs}
			if got := EffectiveStatus(sp); got != c.want {
				t.Errorf("EffectiveStatus = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRollupCountsRefusedApartFromErrors(t *testing.T) {
	a := storageSpan{SpanID: "1", Service: "api", Duration: time.Millisecond,
		StatusCode: "Unset", Kind: "Server",
		Attributes: map[string]string{"http.response.status_code": "404"}}
	b := storageSpan{SpanID: "2", Service: "api", Duration: time.Millisecond,
		StatusCode: "Unset", Kind: "Server",
		Attributes: map[string]string{"http.response.status_code": "500"}}

	got := SelfTimeByService(spans(a, b))
	if len(got) != 1 {
		t.Fatalf("want one service, got %v", got)
	}
	if got[0].ErrorCount != 1 {
		t.Errorf("errors = %d, want 1 (the 500 only)", got[0].ErrorCount)
	}
	if got[0].RefusedCount != 1 {
		t.Errorf("refused = %d, want 1 (the 404)", got[0].RefusedCount)
	}
}
