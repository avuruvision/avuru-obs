//go:build integration

package clickhouse

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// aiSpan extends attrSpan with span EVENTS, which the AI reads need and no
// other test does: the earlier gen_ai convention put message content on events
// rather than on the span, and the content detector has to look there.
type aiSpan struct {
	attrSpan
	eventName  string
	eventAttrs map[string]string
}

func insertAISpans(t *testing.T, s *Store, spans []aiSpan) {
	t.Helper()
	batch, err := s.conn.PrepareBatch(context.Background(), `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, ScopeName, ScopeVersion, SpanAttributes, Duration, StatusCode, StatusMessage,
		 Events.Timestamp, Events.Name, Events.Attributes,
		 Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes)`)
	if err != nil {
		t.Fatalf("preparing batch: %v", err)
	}
	for _, sp := range spans {
		attrs := sp.attrs
		if attrs == nil {
			attrs = map[string]string{}
		}
		evTimes, evNames, evAttrs := []time.Time{}, []string{}, []map[string]string{}
		if sp.eventName != "" {
			ea := sp.eventAttrs
			if ea == nil {
				ea = map[string]string{}
			}
			evTimes, evNames, evAttrs = []time.Time{sp.ts}, []string{sp.eventName}, []map[string]string{ea}
		}
		if err := batch.Append(
			sp.ts, sp.traceID, sp.spanID, sp.parentID, "", sp.name, sp.kind, sp.service,
			map[string]string{"service.name": sp.service}, "test", "1", attrs,
			uint64(sp.duration.Nanoseconds()), sp.status, "",
			evTimes, evNames, evAttrs,
			[]string{}, []string{}, []string{}, []map[string]string{},
		); err != nil {
			t.Fatalf("appending span: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending batch: %v", err)
	}
}

func aiWindow() storage.TimeRange {
	now := time.Now().UTC()
	return storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
}

func llmSpan(id, service string, attrs map[string]string) aiSpan {
	return aiSpan{attrSpan: attrSpan{
		ts: time.Now().UTC().Add(-time.Minute), traceID: "t-" + id, spanID: id,
		name: "chat", kind: "Client", service: service, duration: 200 * time.Millisecond,
		attrs: attrs,
	}}
}

func modelsByName(u storage.AIUsage) map[string]storage.AIModelUsage {
	out := make(map[string]storage.AIModelUsage, len(u.Models))
	for _, m := range u.Models {
		out[m.Model] = m
	}
	return out
}

// The detection rule is the population every other number rests on. An
// ordinary span that happens to carry an attribute called "prompt" is not a
// model call, and counting it would put a service that never called a model on
// the AI screen.
func TestAIModelsCountsOnlyGenAISpans(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("a1", "chat-api", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.system": "openai",
			"gen_ai.response.model": "gpt-4o",
		}),
		// Not a model call: a template renderer with a "prompt" field.
		llmSpan("b1", "cms", map[string]string{"prompt": "hello", "template": "welcome"}),
		// A model call known only by its provider — older instrumentation.
		llmSpan("c1", "search", map[string]string{
			"gen_ai.provider.name": "anthropic", "gen_ai.request.model": "claude-sonnet",
		}),
	})

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	if usage.Total.Calls != 2 {
		t.Errorf("calls = %d, want 2 (the CMS span is not a model call)", usage.Total.Calls)
	}
	models := modelsByName(usage)
	if _, ok := models["gpt-4o"]; !ok {
		t.Errorf("gpt-4o missing: %+v", usage.Models)
	}
	if _, ok := models["claude-sonnet"]; !ok {
		t.Errorf("provider-only call missing: %+v", usage.Models)
	}
}

// The response model wins over the request model, and a row built from the
// request alone is COUNTED as such rather than passing as a measurement of
// what actually ran.
func TestAIModelsPrefersTheRespondingModel(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("a1", "chat", map[string]string{
			"gen_ai.operation.name": "chat",
			"gen_ai.request.model":  "gpt-4o", "gen_ai.response.model": "gpt-4o-2024-08-06",
		}),
		llmSpan("a2", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.request.model": "gpt-4o",
		}),
	})

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	models := modelsByName(usage)
	dated, ok := models["gpt-4o-2024-08-06"]
	if !ok {
		t.Fatalf("the responding model should be the row key: %+v", usage.Models)
	}
	if dated.CallsFromRequestModel != 0 {
		t.Errorf("a call that named its responder is not request-only: %+v", dated)
	}
	alias, ok := models["gpt-4o"]
	if !ok {
		t.Fatalf("the request-only call should still be reported: %+v", usage.Models)
	}
	if alias.CallsFromRequestModel != 1 {
		t.Errorf("request-only calls = %d, want 1", alias.CallsFromRequestModel)
	}
}

// Both token spellings the convention has had are summed. Reading only the
// current one would report real traffic as having spent nothing — invisible,
// because the calls still show up in every other column.
func TestAIModelsReadsBothTokenSpellings(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("a1", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.input_tokens": "100", "gen_ai.usage.output_tokens": "20",
		}),
		llmSpan("a2", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.prompt_tokens": "300", "gen_ai.usage.completion_tokens": "40",
		}),
		// Reported nothing at all: counted as a call, excluded from tokens.
		llmSpan("a3", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
		}),
	})

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	m := modelsByName(usage)["gpt-4o"]
	if m.InputTokens != 400 || m.OutputTokens != 60 {
		t.Errorf("tokens = %d in / %d out, want 400/60", m.InputTokens, m.OutputTokens)
	}
	if m.Calls != 3 {
		t.Errorf("calls = %d, want 3", m.Calls)
	}
	if m.CallsWithoutUsage != 1 {
		t.Errorf("callsWithoutUsage = %d, want 1 — a call that reported nothing is not a call that used nothing", m.CallsWithoutUsage)
	}
}

// Truncation is a SUCCESSFUL call the model cut off. It must not be folded
// into the error count, and it must not disappear.
func TestAIModelsSeparatesTruncationFromFailure(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("a1", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.response.finish_reasons": `["length"]`,
		}),
		llmSpan("a2", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.response.finish_reason": "length",
		}),
		llmSpan("a3", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.response.finish_reasons": `["stop"]`,
		}),
	})

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	m := modelsByName(usage)["gpt-4o"]
	if m.Truncated != 2 {
		t.Errorf("truncated = %d, want 2 (both spellings)", m.Truncated)
	}
	if m.Errors != 0 {
		t.Errorf("errors = %d, want 0 — a truncated answer is not a failed call", m.Errors)
	}
}

// The exposure report. Content is detected from attribute KEYS, on the span or
// on its events — never from an event's NAME, because the gateway's redaction
// empties the attributes and keeps the event. An install that redacts
// correctly must not be told content is leaking.
func TestAIModelsDetectsContentWithoutBeingFooledByEmptyEvents(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		// Content on the span, in the widest-deployed indexed form.
		llmSpan("a1", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.prompt.0.content": "what is the capital of France",
		}),
		// Content on an event, which is where the earlier convention put it.
		{
			attrSpan: llmSpan("a2", "chat", map[string]string{
				"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			}).attrSpan,
			eventName:  "gen_ai.content.prompt",
			eventAttrs: map[string]string{"gen_ai.prompt": "who are you"},
		},
		// Redacted: the event survives with its attributes emptied. This is
		// the case the detector must NOT report.
		{
			attrSpan: llmSpan("a3", "chat", map[string]string{
				"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			}).attrSpan,
			eventName: "gen_ai.content.prompt",
		},
		// A token COUNT under the older spelling. The pattern is anchored so
		// this can never read as a prompt — deleting it would delete the
		// number the module exists to report.
		llmSpan("a4", "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.prompt_tokens": "50",
		}),
	})

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	m := modelsByName(usage)["gpt-4o"]
	if m.Calls != 4 {
		t.Fatalf("calls = %d, want 4", m.Calls)
	}
	if m.CallsWithContent != 2 {
		t.Errorf("callsWithContent = %d, want 2 (span attribute + event attribute only)", m.CallsWithContent)
	}
	if m.InputTokens != 50 {
		t.Errorf("input tokens = %d, want 50 — the older token spelling must survive the content pattern", m.InputTokens)
	}
}

// WITH TOTALS again: a limited model table still has to know the size of the
// tail it is not returning, and the distinct-model count that goes with it.
func TestAIModelsTotalsCoverTheTailBeyondTheLimit(t *testing.T) {
	store := startClickHouse(t)
	var spans []aiSpan
	for _, model := range []string{"m1", "m2", "m3", "m4"} {
		spans = append(spans, llmSpan("s-"+model, "chat", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": model,
			"gen_ai.usage.input_tokens": "10",
		}))
	}
	insertAISpans(t, store, spans)

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow(), Limit: 2})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	if len(usage.Models) != 2 {
		t.Errorf("rows = %d, want the limit of 2", len(usage.Models))
	}
	if usage.Total.Calls != 4 || usage.Total.InputTokens != 40 {
		t.Errorf("totals = %d calls / %d tokens, want 4/40 over the whole window",
			usage.Total.Calls, usage.Total.InputTokens)
	}
	if usage.ModelCount != 4 {
		t.Errorf("modelCount = %d, want 4 — the distinct count, not the row count", usage.ModelCount)
	}
}

// Spend with an owner: the same calls, grouped by who made them.
func TestAICallersGroupsByServiceAndModel(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("a1", "checkout", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.input_tokens": "100",
		}),
		llmSpan("a2", "checkout", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.input_tokens": "50",
		}),
		llmSpan("a3", "search", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.input_tokens": "7",
		}),
	})

	rows, err := store.AICallers(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AICallers: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (checkout and search)", len(rows))
	}
	if rows[0].Service != "checkout" || rows[0].Calls != 2 || rows[0].InputTokens != 150 {
		t.Errorf("busiest caller = %+v, want checkout / 2 calls / 150 tokens", rows[0])
	}

	// And the service filter narrows to one of them, using the same value the
	// row reports — a row is its own filter.
	only, err := store.AICallers(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow(), Service: "search"})
	if err != nil {
		t.Fatalf("AICallers(service): %v", err)
	}
	if len(only) != 1 || only[0].Service != "search" {
		t.Errorf("filtered callers = %+v, want just search", only)
	}
}

// An install that calls no models at all is an empty answer, not an error.
func TestAIEmptyWindowIsNotAnError(t *testing.T) {
	store := startClickHouse(t)
	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	if len(usage.Models) != 0 || usage.Total.Calls != 0 {
		t.Errorf("empty window should be empty: %+v", usage)
	}
	rows, err := store.AICallers(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AICallers: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("empty window should be empty: %+v", rows)
	}
}

// The population is INFERENCE, not "anything wearing a gen_ai attribute".
//
// This is the regression test for the v0.12 defect: aiCallExpr tested that
// gen_ai.operation.name was PRESENT and never read its value, so on an agent
// workload every execute_tool span was counted as a call to a model. It fails
// on the build that shipped v0.11 — one agent turn with two tools reported as
// five model calls rather than two, which is the whole point of writing it.
//
// All four of the totals the defect corrupted are asserted here, because they
// fail for one reason and would otherwise be four separate regressions:
// the call count, the model resolution, the no-usage bucket, and the distinct
// model count.
func TestAIModelsCountsInferenceNotToolsOrAgents(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		// The turn: an agent span containing a model call and two tool calls.
		llmSpan("a1", "assistant", map[string]string{
			"gen_ai.operation.name": "invoke_agent", "gen_ai.system": "openai",
			"gen_ai.agent.name": "researcher",
		}),
		llmSpan("a2", "assistant", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.system": "openai",
			"gen_ai.response.model":      "gpt-4o",
			"gen_ai.usage.input_tokens":  "100",
			"gen_ai.usage.output_tokens": "20",
		}),
		llmSpan("a3", "assistant", map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.system": "openai",
			"gen_ai.tool.name": "search_docs",
		}),
		llmSpan("a4", "assistant", map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.system": "openai",
			"gen_ai.tool.name": "run_sql",
		}),
		// A second, ordinary model call — and the ONLY genuine instrumentation
		// gap in this window: it is a model call that reported no usage.
		llmSpan("b1", "chat-api", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.system": "openai",
			"gen_ai.response.model": "gpt-4o",
		}),
	})

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}

	// 1. Call counts. Two chat spans; the agent span and both tool spans are
	//    not calls to a model. Pre-fix this was 5.
	if usage.Total.Calls != 2 {
		t.Errorf("calls = %d, want 2 (the agent span and 2 tool spans are not model calls)", usage.Total.Calls)
	}

	// 2. The model resolves. A tool span has neither a response nor a request
	//    model, so pre-fix it grouped under the empty model name.
	models := modelsByName(usage)
	if _, ok := models[""]; ok {
		t.Errorf("a row grouped under the empty model name: %+v", usage.Models)
	}
	if got := models["gpt-4o"].Calls; got != 2 {
		t.Errorf("gpt-4o calls = %d, want 2", got)
	}

	// 3. The no-usage bucket names an INSTRUMENTATION GAP. Exactly one model
	//    call here reported no tokens; pre-fix the three non-inference spans
	//    landed in it too, and the honest signal it carries was gone.
	if usage.Total.CallsWithoutUsage != 1 {
		t.Errorf("callsWithoutUsage = %d, want 1 (only the chat call that reported no tokens)",
			usage.Total.CallsWithoutUsage)
	}

	// 4. The distinct-model count. Pre-fix the empty model counted as one.
	if usage.ModelCount != 1 {
		t.Errorf("modelCount = %d, want 1", usage.ModelCount)
	}

	// Tokens come only from the call that reported them.
	if usage.Total.InputTokens != 100 || usage.Total.OutputTokens != 20 {
		t.Errorf("tokens = %d in / %d out, want 100/20", usage.Total.InputTokens, usage.Total.OutputTokens)
	}
}

// The caller table filters to the same population as the model table. They are
// two readings of one set of spans, so a service that only ran tools must not
// appear as a service that called a model.
func TestAICallersCountInferenceOnly(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("c1", "chat-api", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.system": "openai",
			"gen_ai.response.model":      "gpt-4o",
			"gen_ai.usage.input_tokens":  "50",
			"gen_ai.usage.output_tokens": "10",
		}),
		// A service that runs tools and never calls a model.
		llmSpan("d1", "tool-runner", map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.system": "openai",
			"gen_ai.tool.name": "search_docs",
		}),
	})

	callers, err := store.AICallers(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AICallers: %v", err)
	}
	if len(callers) != 1 {
		t.Fatalf("callers = %d rows, want 1: %+v", len(callers), callers)
	}
	if callers[0].Service != "chat-api" {
		t.Errorf("service = %q, want chat-api (tool-runner never called a model)", callers[0].Service)
	}
}

// Embeddings are INFERENCE, deliberately. An embeddings call is a real call to
// a model that spends real tokens; classing it with tools would drop paid
// traffic off the bill. This is the boundary case the roadmap prose originally
// got wrong, so it is pinned here.
func TestAIModelsCountsEmbeddingsAsInference(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("e1", "search", map[string]string{
			"gen_ai.operation.name": "embeddings", "gen_ai.system": "openai",
			"gen_ai.response.model":     "text-embedding-3-small",
			"gen_ai.usage.input_tokens": "800",
		}),
	})

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	if usage.Total.Calls != 1 {
		t.Errorf("calls = %d, want 1 (embeddings is a model call)", usage.Total.Calls)
	}
	if usage.Total.InputTokens != 800 {
		t.Errorf("inputTokens = %d, want 800", usage.Total.InputTokens)
	}
}

// An operation value this build has never heard of stays INFERENCE rather than
// vanishing. The convention keeps adding operations; classifying by an
// allow-list of known inference values would make tomorrow's operation
// invisible — counted in no table and in no total — which is a worse failure
// than mis-labelling it, because nothing on the screen would show it happened.
func TestAIModelsKeepsUnknownOperationsVisible(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("f1", "search", map[string]string{
			"gen_ai.operation.name": "rerank", "gen_ai.system": "cohere",
			"gen_ai.response.model":     "rerank-v3",
			"gen_ai.usage.input_tokens": "40",
		}),
	})

	usage, err := store.AIModels(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AIModels: %v", err)
	}
	if usage.Total.Calls != 1 {
		t.Errorf("calls = %d, want 1 (an unknown operation must not disappear)", usage.Total.Calls)
	}
}

// The tools table reads the population the model table excludes. The two are
// complements over the same window, which is the property the operation-class
// split exists to give them.
func TestAIToolsGroupsByToolNameAndCallers(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("t1", "assistant", map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search_docs",
		}),
		llmSpan("t2", "support-bot", map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search_docs",
		}),
		llmSpan("t3", "assistant", map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "run_sql",
		}),
		// A model call in the same window must not appear in this table.
		llmSpan("t4", "assistant", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
		}),
	})

	tools, err := store.AITools(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AITools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d rows, want 2 (the chat span is not a tool): %+v", len(tools), tools)
	}
	byName := map[string]storage.AIToolUsage{}
	for _, tl := range tools {
		byName[tl.Tool] = tl
	}
	search, ok := byName["search_docs"]
	if !ok {
		t.Fatalf("search_docs missing: %+v", tools)
	}
	if search.Calls != 2 {
		t.Errorf("search_docs calls = %d, want 2", search.Calls)
	}
	if search.CallerCount != 2 {
		t.Errorf("search_docs callerCount = %d, want 2", search.CallerCount)
	}
	if len(search.Callers) != 2 {
		t.Errorf("search_docs callers = %v, want both services", search.Callers)
	}
	if byName["run_sql"].Calls != 1 {
		t.Errorf("run_sql calls = %d, want 1", byName["run_sql"].Calls)
	}
}

// A tool that ran without gen_ai.tool.name is reported under its span name and
// COUNTED as weakly named, rather than dropped. Dropping it would understate
// how much tool work a turn did; reporting it silently would present a span
// name as if the instrumentation had named the tool.
func TestAIToolsFallsBackToTheSpanNameAndSaysSo(t *testing.T) {
	store := startClickHouse(t)
	sp := llmSpan("u1", "assistant", map[string]string{"gen_ai.operation.name": "execute_tool"})
	sp.name = "tools/lookup_customer"
	insertAISpans(t, store, []aiSpan{sp})

	tools, err := store.AITools(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AITools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %d rows, want 1: %+v", len(tools), tools)
	}
	if tools[0].Tool != "tools/lookup_customer" {
		t.Errorf("tool = %q, want the span name", tools[0].Tool)
	}
	if tools[0].NamedBySpan != 1 {
		t.Errorf("namedBySpan = %d, want 1", tools[0].NamedBySpan)
	}
}

// The model filter narrows the model table and leaves the tools table alone. A
// tool span carries no model, so applying it here would report "this model used
// no tools" — false, rather than merely narrow.
func TestAIToolsIgnoreTheModelFilter(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("v1", "assistant", map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search_docs",
		}),
	})

	tools, err := store.AITools(context.Background(), storage.AIQuery{
		Tenant: "default", Range: aiWindow(), Model: "gpt-4o",
	})
	if err != nil {
		t.Fatalf("AITools: %v", err)
	}
	if len(tools) != 1 {
		t.Errorf("tools = %d rows, want 1 (the model filter cannot apply to tools)", len(tools))
	}
}

// A budget is evaluated over the WHOLE month's traffic, not the top rows of a
// screen. Grouped by (service, model) because cost is applied per model from
// declared rates — collapsing to a service total here would leave money with
// nothing to multiply.
func TestAISpendByServiceGroupsByServiceAndModel(t *testing.T) {
	store := startClickHouse(t)
	insertAISpans(t, store, []aiSpan{
		llmSpan("s1", "assistant", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.input_tokens": "100", "gen_ai.usage.output_tokens": "20",
		}),
		llmSpan("s2", "assistant", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.input_tokens": "50", "gen_ai.usage.output_tokens": "10",
		}),
		llmSpan("s3", "assistant", map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "claude-sonnet",
			"gen_ai.usage.input_tokens": "70", "gen_ai.usage.output_tokens": "5",
		}),
		// Tool spans spend no tokens and must not enter a spend total.
		llmSpan("s4", "assistant", map[string]string{
			"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": "search_docs",
		}),
	})

	rows, err := store.AISpendByService(context.Background(), storage.AIQuery{Tenant: "default", Range: aiWindow()})
	if err != nil {
		t.Fatalf("AISpendByService: %v", err)
	}
	byModel := map[string]storage.AIServiceSpend{}
	for _, r := range rows {
		if r.Service != "assistant" {
			t.Errorf("unexpected service %q", r.Service)
		}
		byModel[r.Model] = r
	}
	if len(byModel) != 2 {
		t.Fatalf("rows = %d, want 2 models (the tool span is not spend): %+v", len(byModel), rows)
	}
	if got := byModel["gpt-4o"]; got.InputTokens != 150 || got.OutputTokens != 30 || got.Calls != 2 {
		t.Errorf("gpt-4o = %+v, want 2 calls / 150 in / 30 out", got)
	}
	if got := byModel["claude-sonnet"]; got.InputTokens != 70 {
		t.Errorf("claude-sonnet = %+v, want 70 in", got)
	}
}

// No limit. A budget measured over a truncated table would measure the top N
// services and call it the estate — quietly under every threshold.
func TestAISpendByServiceIsNotLimited(t *testing.T) {
	store := startClickHouse(t)
	var spans []aiSpan
	for i := 0; i < 60; i++ {
		spans = append(spans, llmSpan(fmt.Sprintf("l%d", i), fmt.Sprintf("svc-%02d", i), map[string]string{
			"gen_ai.operation.name": "chat", "gen_ai.response.model": "gpt-4o",
			"gen_ai.usage.input_tokens": "10",
		}))
	}
	insertAISpans(t, store, spans)

	// Limit is deliberately set: the query must ignore it, since a budget is
	// not a screen.
	rows, err := store.AISpendByService(context.Background(), storage.AIQuery{
		Tenant: "default", Range: aiWindow(), Limit: 5,
	})
	if err != nil {
		t.Fatalf("AISpendByService: %v", err)
	}
	if len(rows) != 60 {
		t.Errorf("rows = %d, want all 60 services", len(rows))
	}
}
