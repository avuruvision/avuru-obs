package clickhouse

import (
	"context"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The gen_ai reads (module ai). Everything here runs over otel_traces: an
// instrumented application's model calls arrive as ordinary spans carrying the
// OpenTelemetry gen_ai attributes, so there is no schema, no new table and no
// metrics dependency. See design/2026-08-27-ai-observability.md.
//
// Prices never appear in this file. Cost is applied by the API layer from the
// operator's declared rates, the same separation green keeps for its carbon
// factors: SQL returns what was measured, callers apply what was declared.

// aiCallExpr is the one definition of "this span is a model call". Everything
// else — the model table, the caller view, the summary — filters on it, so
// they cannot describe different populations.
//
// The operation is the strongest marker (it is required by the convention);
// the provider is accepted as a fallback because instrumentations that predate
// the operation attribute still set it, and both spellings of the provider are
// in production right now.
const aiCallExpr = `(SpanAttributes['gen_ai.operation.name'] != '' ` +
	`OR SpanAttributes['gen_ai.system'] != '' ` +
	`OR SpanAttributes['gen_ai.provider.name'] != '')`

// aiOperationClass names what KIND of gen_ai span a row is. aiCallExpr above
// answers "is this span gen_ai at all"; this answers "gen_ai *what*", which is
// the question v0.11 never asked — and the reason its four totals were wrong on
// any workload that runs tools.
//
// The convention gives gen_ai.operation.name a set of well-known values, and
// `chat` is only one of them. A compliant agent instrumentation emits a span
// with `execute_tool` for every tool it runs; counting those as model calls
// inflates the call count, ranks a database lookup against a completion in the
// latency quantiles, resolves the model to the empty string, and fills the
// no-usage bucket with spans that were never model calls.
type aiOperationClass int

const (
	// aiInference is a call to a model: chat, text_completion,
	// generate_content, embeddings — and the empty/unknown case, where only
	// gen_ai.system or gen_ai.provider.name identified the span.
	aiInference aiOperationClass = iota
	// aiTool is one tool execution inside an agent turn.
	aiTool
	// aiAgentOp is an agent lifecycle span: create_agent, invoke_agent. It
	// CONTAINS the model and tool calls of a turn, so counting it beside them
	// would double-count the turn.
	aiAgentOp
)

// The class predicates. Note the asymmetry, which is deliberate: tool and agent
// are ALLOW-lists over the two operation values each owns, and inference is
// their COMPLEMENT rather than an allow-list of its own.
//
// Enumerating inference instead would be the more obvious spelling and the more
// dangerous one: the convention keeps adding operations (generate_content is
// itself a recent arrival), and an install emitting one this build has not
// heard of would have its traffic classified as nothing at all — counted in no
// table, in no total, and visible nowhere. Traffic that silently vanishes is
// exactly the failure v0.11 built the no-usage bucket to avoid. The complement
// mis-labels an unknown operation as inference, which is wrong in a way that is
// visible and recoverable; the allow-list makes it disappear.
const (
	aiToolOpExpr  = `(SpanAttributes['gen_ai.operation.name'] = 'execute_tool')`
	aiAgentOpExpr = `(SpanAttributes['gen_ai.operation.name'] IN ('create_agent', 'invoke_agent'))`
	// Everything that is a gen_ai span and is neither a tool nor an agent span.
	aiInferenceOpExpr = `(NOT ` + aiToolOpExpr + ` AND NOT ` + aiAgentOpExpr + `)`
)

// classExpr returns the predicate selecting this class alone.
func (c aiOperationClass) classExpr() string {
	switch c {
	case aiTool:
		return aiToolOpExpr
	case aiAgentOp:
		return aiAgentOpExpr
	default:
		return aiInferenceOpExpr
	}
}

// aiModelExpr resolves the model a call actually used.
//
// The RESPONSE model wins over the request model: an alias resolves at the
// provider (asking for "gpt-4o" is answered by a dated build), and the
// response model is what a bill is computed against. Falling back the other
// way would report an alias as if it were the model that ran.
const aiModelExpr = `if(SpanAttributes['gen_ai.response.model'] != '', ` +
	`SpanAttributes['gen_ai.response.model'], SpanAttributes['gen_ai.request.model'])`

// aiRequestOnlyExpr is true when only the requested model is known. Counted
// rather than hidden: a row built from request models is a weaker claim than
// one built from what answered, and the screen says which it is looking at.
const aiRequestOnlyExpr = `(SpanAttributes['gen_ai.response.model'] = '' ` +
	`AND SpanAttributes['gen_ai.request.model'] != '')`

// aiProviderExpr reads whichever spelling of the provider the instrumentation
// used. `gen_ai.system` is the older name and still the more common one.
const aiProviderExpr = `if(SpanAttributes['gen_ai.system'] != '', ` +
	`SpanAttributes['gen_ai.system'], SpanAttributes['gen_ai.provider.name'])`

// Token counts, under both spellings the convention has had. Reading only the
// current one would report a large share of real traffic as having spent no
// tokens at all — which is not a small error but an invisible one, since the
// calls still appear in every other column.
const (
	aiInputTokensExpr = `toUInt64OrZero(if(SpanAttributes['gen_ai.usage.input_tokens'] != '', ` +
		`SpanAttributes['gen_ai.usage.input_tokens'], SpanAttributes['gen_ai.usage.prompt_tokens']))`
	aiOutputTokensExpr = `toUInt64OrZero(if(SpanAttributes['gen_ai.usage.output_tokens'] != '', ` +
		`SpanAttributes['gen_ai.usage.output_tokens'], SpanAttributes['gen_ai.usage.completion_tokens']))`
	// aiHasUsageExpr separates "spent no tokens" from "never said". A call
	// that reported nothing must not be averaged in as a zero.
	aiHasUsageExpr = `(SpanAttributes['gen_ai.usage.input_tokens'] != '' ` +
		`OR SpanAttributes['gen_ai.usage.prompt_tokens'] != '' ` +
		`OR SpanAttributes['gen_ai.usage.output_tokens'] != '' ` +
		`OR SpanAttributes['gen_ai.usage.completion_tokens'] != '')`
)

// aiTruncatedExpr: the model stopped because it hit the token ceiling. This is
// NOT an error — the call succeeded and the response is real — but it is the
// most common reason an answer comes back malformed, so it gets a state of its
// own beside the three the rest of the product uses.
//
// `finish_reasons` is an array and reaches ClickHouse as its rendered form, so
// it is matched by substring; the singular spelling some instrumentations emit
// is matched exactly.
const aiTruncatedExpr = `(position(SpanAttributes['gen_ai.response.finish_reasons'], 'length') > 0 ` +
	`OR SpanAttributes['gen_ai.response.finish_reason'] = 'length')`

// aiContentKeyPattern matches the attribute keys that carry prompt or
// completion TEXT, across the spellings the convention has used and the
// indexed form (`gen_ai.prompt.0.content`) the widest-deployed instrumentation
// emits.
//
// It is anchored so that `gen_ai.usage.prompt_tokens` — a token COUNT under
// the older spelling — can never match it. Deleting that would delete the
// number this module exists to report.
//
// The gateway's redaction stage carries the same pattern as OTTL
// (deploy/helm/avuruobs/templates/gateway-config.yaml, `transform/genai`).
// The two must agree: this one decides whether to warn that content is
// arriving, that one decides whether it is stored at all.
const aiContentKeyPattern = `^gen_ai\\.(prompt|completion|input\\.messages|output\\.messages|system_instructions|content)`

// aiHasContentExpr is true when a call still carries message text — on the
// span itself, or on one of its events, which is where the earlier convention
// put it.
//
// Detection reads attribute KEYS, never event names: redaction empties the
// attributes and keeps the event, so an install that redacts correctly still
// has `gen_ai.content.prompt` events and must not be told content is leaking.
const aiHasContentExpr = `(arrayExists(k -> match(toString(k), '` + aiContentKeyPattern + `'), mapKeys(SpanAttributes)) ` +
	`OR arrayExists(m -> arrayExists(k -> match(toString(k), '` + aiContentKeyPattern + `'), mapKeys(m)), Events.Attributes))`

// aiFilters appends the shared WHERE clauses — the same filter vocabulary the
// trace search uses, so the AI screen and the Traces screen describe the same
// traffic when set to the same window.
//
// The class is taken here rather than on storage.AIQuery on purpose. Every
// query has exactly one population it means — the model and caller tables are
// inference, the tools table is tools — so a class knob on the query would let
// a caller ask for a mixture that answers no question, and would put this
// defect back within reach of the API layer. One choke point, one decision:
// the AEP rejects filtering per query as "four call sites, four chances to
// forget the fifth".
func aiFilters(query string, q storage.AIQuery, class aiOperationClass, args []any) (string, []any) {
	query += " AND " + aiCallExpr + " AND " + class.classExpr()
	if q.Service != "" {
		query += " AND ServiceName = ?"
		args = append(args, q.Service)
	}
	if q.Model != "" {
		query += " AND " + aiModelExpr + " = ?"
		args = append(args, q.Model)
	}
	query, args = tagFilters(query, q.Tags, args)
	if q.ExcludeAux {
		query += auxExclusion("")
	}
	return query, args
}

func aiLimit(n int) int {
	if n <= 0 || n > 200 {
		return 50
	}
	return n
}

// AIModels returns per-model usage plus the totals over EVERY model call in
// the window, including the ones past the limit.
//
// The totals come from WITH TOTALS, computed before LIMIT, so the summary and
// the table are the same read: they cannot disagree about how many calls the
// window held, which is the failure mode of computing a summary separately.
// `uniqExact` over the model expression is 1 in every grouped row and the true
// distinct-model count in the totals row.
func (s *Store) AIModels(ctx context.Context, q storage.AIQuery) (storage.AIUsage, error) {
	query := `
SELECT
    ` + aiModelExpr + `                             AS model,
    anyIf(` + aiProviderExpr + `, ` + aiProviderExpr + ` != '') AS provider,
    count()                                         AS calls,
    countIf(` + errorSpanExpr("") + `)              AS errors,
    countIf(` + refusedSpanExpr("") + `)            AS refused,
    countIf(` + aiTruncatedExpr + `)                AS truncated,
    countIf(NOT ` + aiHasUsageExpr + `)             AS noUsage,
    countIf(` + aiRequestOnlyExpr + `)              AS requestOnly,
    countIf(` + aiHasContentExpr + `)               AS withContent,
    sum(` + aiInputTokensExpr + `)                  AS inTokens,
    sum(` + aiOutputTokensExpr + `)                 AS outTokens,
    uniqExact(` + aiModelExpr + `)                  AS models,
    quantiles(0.5, 0.95, 0.99)(toFloat64(Duration)) AS qs
FROM otel_traces
WHERE Tenant IN (?)
  AND Timestamp >= ? AND Timestamp < ?`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End}
	query, args = aiFilters(query, q, aiInference, args)
	query += `
GROUP BY model WITH TOTALS
ORDER BY calls DESC, model ASC
LIMIT ?`
	args = append(args, aiLimit(q.Limit))

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return storage.AIUsage{}, fmt.Errorf("ai models: %w", err)
	}
	defer rows.Close()

	var out storage.AIUsage
	for rows.Next() {
		m, _, err := scanAIModel(rows.Scan)
		if err != nil {
			return storage.AIUsage{}, err
		}
		out.Models = append(out.Models, m)
	}
	if err := rows.Err(); err != nil {
		return storage.AIUsage{}, err
	}
	// A window that matched nothing has no totals row to read. That is an
	// empty answer, not a failed one.
	if total, models, err := scanAIModel(rows.Totals); err == nil {
		out.Total = total
		out.ModelCount = models
	}
	return out, nil
}

// scanAIModel converts one row into a model's usage. It takes the scan
// FUNCTION so the grouped rows and the WITH TOTALS row — identical in shape,
// reached through different methods — share one conversion.
func scanAIModel(scan func(dest ...any) error) (storage.AIModelUsage, uint64, error) {
	var (
		m      storage.AIModelUsage
		models uint64
		quant  []float64
	)
	if err := scan(&m.Model, &m.Provider, &m.Calls, &m.Errors, &m.Refused, &m.Truncated,
		&m.CallsWithoutUsage, &m.CallsFromRequestModel, &m.CallsWithContent,
		&m.InputTokens, &m.OutputTokens, &models, &quant); err != nil {
		return m, 0, fmt.Errorf("scanning ai model row: %w", err)
	}
	m.P50, m.P95, m.P99 = nsQuantiles(quant)
	return m, models, nil
}

// AICallers returns usage per (calling service, model) — spend with an owner.
//
// A model table says what the estate is paying for; this says who to talk to
// about it. It is deliberately a separate read rather than a second grouping
// dimension on the same one: the two are read together on the same screen, and
// a query that has to serve both ends up honest about neither.
func (s *Store) AICallers(ctx context.Context, q storage.AIQuery) ([]storage.AICallerUsage, error) {
	query := `
SELECT
    ServiceName                          AS service,
    ` + aiModelExpr + `                  AS model,
    count()                              AS calls,
    countIf(` + errorSpanExpr("") + `)   AS errors,
    countIf(` + aiTruncatedExpr + `)     AS truncated,
    countIf(NOT ` + aiHasUsageExpr + `)  AS noUsage,
    sum(` + aiInputTokensExpr + `)       AS inTokens,
    sum(` + aiOutputTokensExpr + `)      AS outTokens
FROM otel_traces
WHERE Tenant IN (?)
  AND Timestamp >= ? AND Timestamp < ?`
	args := []any{tenantsOrDefault(q.Tenants, q.Tenant), q.Range.Start, q.Range.End}
	query, args = aiFilters(query, q, aiInference, args)
	query += `
GROUP BY service, model
ORDER BY calls DESC, service ASC, model ASC
LIMIT ?`
	args = append(args, aiLimit(q.Limit))

	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ai callers: %w", err)
	}
	defer rows.Close()

	var out []storage.AICallerUsage
	for rows.Next() {
		var c storage.AICallerUsage
		if err := rows.Scan(&c.Service, &c.Model, &c.Calls, &c.Errors, &c.Truncated,
			&c.CallsWithoutUsage, &c.InputTokens, &c.OutputTokens); err != nil {
			return nil, fmt.Errorf("scanning ai caller row: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
