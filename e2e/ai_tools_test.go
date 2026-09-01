//go:build e2e

// The AI module's tools table against the seeded compose stack.
//
// The seed holds one agent turn (deploy/compose/seed/fixtures/traces_genai.json):
// an invoke_agent span containing two model calls and four tool executions, of
// which search_docs runs twice and one carries no gen_ai.tool.name. That shape
// is what makes this suite able to check the v0.12 defect end to end rather
// than only in SQL — before operation classes, all six of those spans counted
// as calls to a model.
package e2e

import (
	"fmt"
	"testing"
	"time"
)

type aiTool struct {
	Tool        string   `json:"tool"`
	Calls       uint64   `json:"calls"`
	Errors      uint64   `json:"errors"`
	NamedBySpan uint64   `json:"namedBySpan"`
	Callers     []string `json:"callers"`
	CallerCount uint64   `json:"callerCount"`
	P95Ms       float64  `json:"p95Ms"`
}

type aiToolsResp struct {
	Tools              []aiTool `json:"tools"`
	ModelFilterIgnored bool     `json:"modelFilterIgnored"`
}

type aiModel struct {
	Model             string `json:"model"`
	Calls             uint64 `json:"calls"`
	CallsWithoutUsage uint64 `json:"callsWithoutUsage"`
}

type aiModelsResp struct {
	Models []aiModel `json:"models"`
	Total  aiModel   `json:"total"`
}

func aiWindowQuery() string {
	return "from=" + time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339) +
		"&to=" + time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
}

// The tools endpoint reports the seeded turn's tool executions, and collapses
// the repeated tool into one row with a count.
func TestAIToolsReportsTheSeededAgentTurn(t *testing.T) {
	var resp aiToolsResp
	poll(t, 60*time.Second, func() error {
		if err := getJSON("/api/v1/ai/tools?"+aiWindowQuery(), &resp); err != nil {
			return err
		}
		if len(resp.Tools) == 0 {
			return fmt.Errorf("no tools reported yet")
		}
		return nil
	})

	byName := map[string]aiTool{}
	for _, tl := range resp.Tools {
		byName[tl.Tool] = tl
	}

	search, ok := byName["search_docs"]
	if !ok {
		t.Fatalf("search_docs missing from %+v", resp.Tools)
	}
	// Two executions of the same tool are ONE row with a count — the loop is
	// the thing worth seeing, not two identical lines.
	if search.Calls != 2 {
		t.Errorf("search_docs calls = %d, want 2", search.Calls)
	}
	if search.CallerCount != 1 || len(search.Callers) != 1 {
		t.Errorf("search_docs callers = %v (count %d), want just seed-assistant",
			search.Callers, search.CallerCount)
	}
	if search.P95Ms <= 0 {
		t.Errorf("search_docs p95Ms = %v, want a real latency", search.P95Ms)
	}

	if _, ok := byName["run_sql"]; !ok {
		t.Errorf("run_sql missing from %+v", resp.Tools)
	}

	// The tool whose instrumentation set no gen_ai.tool.name is reported under
	// its span name and counted as weakly named rather than dropped.
	fallback, ok := byName["tools/lookup_customer"]
	if !ok {
		t.Fatalf("span-named tool missing from %+v", resp.Tools)
	}
	if fallback.NamedBySpan != fallback.Calls {
		t.Errorf("namedBySpan = %d, want all %d calls flagged", fallback.NamedBySpan, fallback.Calls)
	}
}

// The defect, end to end. Tool and agent spans must not reach the model table:
// before operation classes the seeded turn alone added five non-model rows to
// it, resolved under an empty model name, and filled the no-usage bucket.
func TestAIModelsExcludeToolAndAgentSpans(t *testing.T) {
	var resp aiModelsResp
	poll(t, 60*time.Second, func() error {
		if err := getJSON("/api/v1/ai/models?"+aiWindowQuery(), &resp); err != nil {
			return err
		}
		if resp.Total.Calls == 0 {
			return fmt.Errorf("no model calls reported yet")
		}
		return nil
	})

	for _, m := range resp.Models {
		if m.Model == "" {
			t.Errorf("a row grouped under the empty model name: %+v", resp.Models)
		}
	}

	// Every seeded model call reports token usage, so the no-usage bucket must
	// be empty. Pre-fix the turn's four tool spans and its agent span landed in
	// it, which is exactly the honest signal the bucket exists to carry.
	if resp.Total.CallsWithoutUsage != 0 {
		t.Errorf("callsWithoutUsage = %d, want 0 — tool spans must not land in the instrumentation-gap bucket",
			resp.Total.CallsWithoutUsage)
	}
}

// A model filter narrows the model table and cannot narrow the tools table.
// The response says so rather than returning an empty table that silently
// disagrees with the filter bar above it.
func TestAIToolsSaysTheModelFilterDidNotApply(t *testing.T) {
	var resp aiToolsResp
	poll(t, 60*time.Second, func() error {
		return getJSON("/api/v1/ai/tools?"+aiWindowQuery()+"&model=gpt-4o-2024-08-06", &resp)
	})
	if !resp.ModelFilterIgnored {
		t.Error("modelFilterIgnored not set despite a model filter")
	}
	if len(resp.Tools) == 0 {
		t.Error("tools table emptied by a filter that cannot apply to it")
	}
}
