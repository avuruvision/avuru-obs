# AEP: Agents, budgets, and one rate table — the spend you can act on

- **Date:** 2026-08-30
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

v0.11 taught the product to read the model calls already in its trace store and
report them as rows. This proposes the three changes that turn that report into
something an operator can act on, all of them still reading spans the wedge
already stores:

1. **Agents and tool calls as a shape.** An agent turn is a graph — a model
   call that fans out to tools and comes back — and the product currently sees
   it as a flat list of spans. Worse, it miscounts it: `execute_tool` spans are
   being counted as model calls today.
2. **Token budgets and an alert rule**, on the pattern green already proved for
   carbon — spend is the number people want a threshold on.
3. **One rate table, authored in the UI.** The product now has two of them, in
   two different mechanisms, and neither can be edited without a `helm
   upgrade`.

Like v0.11, this adds **no collection**: no receiver, no agent, no new
container, and — for the first two items — no schema. An install that upgrades
sees its history, not just what arrives next.

## Motivation

### The reading defect, first

`hub/internal/storage/clickhouse/ai.go` decides what counts as a model call:

```go
const aiCallExpr = `(SpanAttributes['gen_ai.operation.name'] != '' ` +
	`OR SpanAttributes['gen_ai.system'] != '' ` +
	`OR SpanAttributes['gen_ai.provider.name'] != '')`
```

It tests that `gen_ai.operation.name` is **present**. It never looks at the
value. But the OpenTelemetry GenAI conventions give that attribute a set of
well-known values, and `chat` is only one of them — `embeddings`,
`create_agent`, `invoke_agent` and `execute_tool` are the others. A compliant
agent instrumentation emits a span with `gen_ai.operation.name=execute_tool`
for every tool it runs.

So on an agent workload, every tool execution is currently counted as a model
call. Four consequences, none of them visible on the screen:

- **Call counts inflate.** One agent turn that consults four tools reports as
  five model calls.
- **Latency mixes two populations.** The p50/p95/p99 on the model table are
  `quantiles(...)(Duration)` over both inference spans and tool spans — a
  database lookup and a completion, ranked together.
- **The `noUsage` bucket stops meaning what it says.** A tool span reports no
  token usage, so it lands in `countIf(NOT aiHasUsageExpr)`. That bucket exists
  to name an *instrumentation gap* — v0.11 deliberately counted those calls
  rather than averaging them in as zeros. On an agent workload it fills instead
  with spans that were never model calls, and the honest signal it was built to
  carry is gone.
- **The model resolves to nothing.** `aiModelExpr` falls back from response
  model to request model; a tool span has neither, so it groups under an empty
  model name.

This is precisely the class of error the v0.11 AEP was written to guard
against — it lists four "ways of being confidently wrong" and closes each one.
This is the fifth, and it slipped through because at the time the question was
*which* model answered, not *whether the span was a model call at all*.

It is worth being exact about the blast radius: an install whose applications
make plain chat completions and no tool calls is unaffected, because no span
carries `execute_tool`. The defect appears exactly when someone runs the agent
workloads this AEP is otherwise about — which is why the fix and the feature
belong in one change rather than two.

### The shape

Once tool spans are told apart from model calls, they are worth drawing. An
agent turn is not a table; it is a small graph — a model call that decides, a
fan-out to tools, results returning, often another model call after. The
questions an operator has about it are graph questions: which tool is slow,
which one fails, how many hops a turn takes before it converges, and which
tool a retry loop is stuck on.

The renderer already exists. The **Path** view built in #152 draws the
service-level graph of a single request, weighted by the time spent *inside*
each node rather than by span duration — which is exactly the weighting an
agent turn needs, since the model-call span contains its tool spans.

### The threshold

The AI screen answers "what did we spend". Nobody watches a screen. Green
already solved this shape for carbon: `hub/internal/green/budgets.go` is a
**pure** state machine — given the config, the month-to-date usage per group,
the previous alerting state and the clock, it returns the next state and the
notifications to send. It writes into the shared `alert_state` and
`alert_history` behind a rule-key prefix (`BudgetRulePrefix = "green:"`) so the
alerting tick can recognise its rows, and `internal/alerting` stays unedited by
contract. Its tick lives at `hub/cmd/hub/green_budgets.go`.

Spend wants the same machine with a different unit.

### Two rate tables, two mechanisms

The roadmap deferred UI-authored prices until "either of them needs a second
rate table". They already differ more than the roadmap assumed — not just in
content but in how they are loaded:

| | AI prices | Cost rates |
|---|---|---|
| Shape | `[]Price{Model, InputPer1MTokens, OutputPer1MTokens}` + `Currency` | `{CPUCoreHour, MemGiBHour, Currency}` |
| Source | mounted ConfigMap, `AVURUOBS_AI_CONFIG` | three env vars, `AVURUOBS_COST_*` |
| Reload | hot, via `loadHotReload` | **read once at startup** |
| Validation | fail-loud `ParseConfig` | `envFloatOr(..., 0)` |
| Home | `hub/internal/ai/config.go` | `hub/internal/api/cost.go` |

So the same operator declaring what their estate costs does it twice, in two
formats, and one of the two takes a pod restart. Two independent `Currency`
fields can also disagree, and nothing notices.

Ties to the [wedge](../AGENTS.md): nothing here changes what is collected or
how fast a fresh cluster reaches a service map. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
the hub reads via SQL, the module framework gates the surface, rates stay
operator-declared because **there is no pricing API** — it would be the first
outbound call in a product whose promise is that nothing leaves the cluster —
and the overlay storage that item 3 needs already exists.

### Goals

- **Tell tool and agent spans from model calls**, and fix the four totals that
  currently mix them.
- **Draw an agent turn as the graph it is**, reusing the Path renderer.
- **Alert on spend** — tokens or money — with green's budget machine, not a
  second one.
- **One rate table**, authored in the UI, hot, validated, with chart-declared
  values still readable and marked read-only.
- **Nothing new collected.** Items 1 and 2 add no schema at all.

### Non-goals

- **Cost joined to green.** The natural next rider, and still blocked: green's
  Kepler/RAPL read has been unverified on real hardware since v0.2
  ([runbook](../docs/runbooks/green-rapl-validation.md)), and joining it to
  cost's CI-proven numbers would launder that doubt.
- **Reading a second control plane.** Linkerd publishes no counterpart to
  `pilot_total_xds_rejects`; a design question needing an operator who runs
  one, not a mapping table.
- **Root-cause summaries from a model.** Deliberately still out — it would be
  the first outbound call, which is a release-level decision, not a corner of
  one.
- **Storing prompts or tool arguments.** v0.11 decided this: content is dropped
  at the gateway by default, ungated by the module. A tool call's *name* and
  timing are structure; its arguments are content, and this AEP does not
  reopen that.
- **A bundled price table.** Stale within a month while looking exactly as
  authoritative as a number you typed yourself.

## Solution

### 1. Operation classes, then the shape

Introduce an operation-class expression beside `aiCallExpr`, splitting the
`gen_ai.*` population three ways from the *value* of `gen_ai.operation.name`:

- **inference** — `chat`, `text_completion`, `generate_content`, `embeddings`,
  and the empty/unknown case where only `gen_ai.system` or
  `gen_ai.provider.name` identified the span. This is what today's model table,
  caller table and summary should have been reading all along.
- **tool** — `execute_tool`.
- **agent** — `create_agent`, `invoke_agent`.

Every existing AI query filters to **inference**, which restores `noUsage`,
the latency quantiles, the call counts and the model resolution to the
population they were designed for. The unknown case stays with inference
deliberately: it is where pre-convention instrumentation lands, and moving it
would silently drop traffic v0.11 goes out of its way to count.

Then two additions:

- `GET /api/v1/ai/tools` — per tool name (`gen_ai.tool.name`, falling back to
  the span name): calls, failures, latency, and the callers invoking it.
- An **agent turn** view on the trace: the Path renderer, scoped to one
  `invoke_agent` span's subtree, with model calls and tool calls as distinct
  node kinds and self-time weighting. A tool a turn hit four times is one node
  with a count, not four — the loop is the thing worth seeing.

### 2. Token budgets

A `budgets` block in the AI config, mirroring green's `Budget` field for field:

```go
type Budget struct {
	Name       string  `json:"name"`
	Scope      string  `json:"scope"`      // a calling service, or "" for the estate
	MonthlyTokens int64 `json:"monthlyTokens,omitempty"`
	MonthlyCost   float64 `json:"monthlyCost,omitempty"`
	WarnRatio  float64 `json:"warnRatio,omitempty"` // 0 → 0.8
	Channel    string  `json:"channel,omitempty"`
}
```

`EvaluateBudgets` in `internal/ai` is green's function with the unit changed:
same pure signature, same `ok`/`warn`/`exceeded` vocabulary, same straight
`ok→firing` transition with no pending step, writing behind `BudgetRulePrefix
= "ai:"`. The tick mirrors `hub/cmd/hub/green_budgets.go`. `internal/alerting`
is not edited, exactly as green's contract requires.

Exactly one of `MonthlyTokens` or `MonthlyCost` may be set, and a cost budget
is **refused at parse time when the models in scope are not all priced** —
fail-loud, like the rest of the config. A budget that silently measures against
a floor is worse than no budget: it would come in under every threshold by
being ignorant of half the spend.

### 3. One rate table

A `rates` object in the overlay store — the mechanism runtime collection
control already established: overlay storage, closed-schema validation, and
`GET`/`PUT`/`DELETE` under `/api/v1/rates`, admin-gated, default-off flag
absent because there is no cluster-mutating half here.

The precedent for the read model is service groups (#2026-08-07): **chart
values stay readable and read-only, UI-authored entries overlay them, and the
screen says which is which.** Both existing tables move behind one resolver so
the API and the budget evaluator cannot disagree about a price — the same
lesson service groups taught when the alerting evaluator turned out to be
reading different config than the API served.

Cost's env vars gain hot-reload by moving to that resolver. The two `Currency`
fields become one, and an install that set them differently gets a startup
warning naming both rather than a silent pick.

### Alternatives considered

- **Exclude `execute_tool` in each query.** Four call sites, four chances to
  forget the fifth. The class expression is one definition every query filters
  through, which is the shape `aiCallExpr` already uses for exactly this
  reason.
- **A `gen_ai` materialized view.** Faster reads, bought with a migration, a
  second copy of rows already stored, and a forked retention story. Rejected in
  the v0.11 AEP at this size; nothing here changes the size.
- **Treat tools as a fourth column on the model table.** Cheaper, and it
  answers "how much tool" without answering any of the graph questions — which
  tool, called by which turn, in what order, how many times.
- **A second budget engine for spend.** Two state machines writing the same
  `alert_state` table with different transition rules is how the firing/pending
  semantics drift apart. Green's is already pure and already shares that table.
- **Prices in the UI *only*.** Breaks GitOps installs that declare everything
  in values. Chart-declared stays authoritative-and-read-only, as with groups.
- **Leave cost's rates on env vars.** Keeps two mechanisms and the restart, and
  guarantees the currencies drift.

## Verification

- **The defect, first and separately.** A regression test that inserts
  `execute_tool` and `invoke_agent` spans beside chat spans and asserts the
  model table, the caller table and the summary count only the chat ones — and
  that `noUsage` counts the instrumentation gap it was built for, not the tool
  spans. This test must **fail on `main`**; that is what makes it a fix rather
  than a claim.
- **Budgets** as green's are tested: table-driven cases over the pure
  `EvaluateBudgets` — under, crossing warn, crossing the budget, recovering —
  with no database. Plus one integration case proving `ai:`-prefixed rows
  survive a failed usage recompute rather than being clobbered to `ok`, which
  is the bug `BudgetRulePrefix` exists to prevent.
- **A cost budget over unpriced models is refused at parse time**, asserted on
  the config, not the screen.
- **Rates** through the overlay's existing closed-schema validation tests, plus
  a case proving a chart-declared rate is served read-only and a UI-authored
  one overlays it.
- **e2e**: the agent-turn view over seeded agent spans in `e2e-ui`; the tools
  endpoint in the Go suite.
- **The wedge is untouched** — no chart component changes, so `e2e-helm` and
  the TTV gate should be unaffected. That they are is the check.

## Roadmap

- [ ] AEP accepted
- [ ] Operation classes + the four totals corrected (the fix, on its own)
- [ ] `GET /api/v1/ai/tools` + the tools table
- [ ] Agent-turn Path view
- [ ] Token/cost budgets + the tick
- [ ] One rate resolver, `/api/v1/rates`, and the Settings surface
- [ ] Docs: bilingual changelog, feature-status matrix, API reference
