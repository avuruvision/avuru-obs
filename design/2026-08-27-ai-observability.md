# AEP: AI observability — the model calls you are already sending

- **Date:** 2026-08-27
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

Add an **ai** module that reads the `gen_ai.*` spans an instrumented
application already sends, and answers the three questions a trace list cannot:
which models are being called and by what, what those calls cost in tokens and
money, and how they behave — latency, failures, and output that was cut off. It
owns no schema, adds no receiver, and collects nothing new.

It ships with the product's first **position on model content**. Prompts and
completions are stored and displayed today, by accident rather than by
decision; from this release the gateway drops them by default.

## Motivation

Applications in this estate are calling models. Because the OpenTelemetry
`gen_ai` conventions describe those calls as ordinary spans, they are *already
arriving* — every LLM SDK wrapper in common use emits them, and the wedge
stores every span it is sent. The product can show one of them in a waterfall.
It cannot say that four fifths of the token spend is one model behind one
route, that a model swap doubled p95, or that the service quietly retrying on
truncation is the one whose bill moved.

That is a reporting gap, and it would be an ordinary feature. What makes this a
release-level decision is the second half.

### The exposure nobody chose

Three facts, each of them true of `main` today:

1. The gateway's trace pipeline is `[tenant?, batch]`. There is no redaction
   stage anywhere in it.
2. `otel_traces` stores `SpanAttributes` and `Events.Attributes` verbatim, and
   `hub migrate` applies the same TTL to them as to everything else.
3. The trace detail panel renders both as attribute tables, to anyone holding
   the Viewer role.

So on an install whose application has content capture switched on — the
default in more than one popular instrumentation — **user prompts and model
completions are sitting in ClickHouse for the full trace retention and are two
clicks from the trace list.** No feature of this product put them there, and no
feature of this product will help you notice. Nobody decided this. This AEP
decides it.

Ties to the [wedge](../AGENTS.md): no application change, no new agent, no new
container, and the read is over the table the wedge fills in the first five
minutes. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
reuse over rewrite — the `transform` processor the redaction needs is already
in the distro — the hub reads via SQL, the module framework gates the surface,
and nothing leaves the cluster.

### Goals

- **Per model**: calls, input and output tokens, latency distribution,
  failures, truncation, and a cost the operator priced.
- **Per caller**: which service asks which model, so spend attaches to a team
  rather than to the estate.
- **Coverage told, not averaged**: calls the module could not read are named in
  a bucket of their own.
- **A content position**: redacted at the gateway by default, never rendered on
  this screen, and *reported* when it arrives anyway.

### Non-goals

- **Evaluation or quality scoring.** Nothing here judges an output. That needs
  ground truth this product does not have and should not invent.
- **A prompt and completion viewer.** Not in this AEP, and not behind a flag in
  it — see [Alternatives](#alternatives-considered).
- **Billing reconciliation.** Cost is estimated from token counts and declared
  prices. It is an engineering signal, not an invoice — the same line the cost
  module draws.
- **Chasing every vendor's private attribute namespace.** One convention is
  read, plus the token spellings that convention itself changed.
- **Agent and tool-call topology.** A follow-up, listed below.

## Solution

### What counts as an AI call

One rule, in one place: a span carrying `gen_ai.operation.name`, or failing
that a provider (`gen_ai.system`, or `gen_ai.provider.name` on the newer
spelling). Everything else follows from that predicate, so the summary, the
model table and the caller view cannot disagree about the population they
describe.

Three readings need care, because each has a wrong answer that looks right:

- **Model** is `gen_ai.response.model`, falling back to
  `gen_ai.request.model`. Response first: it is what actually served the call
  (an alias resolves — `gpt-4o` answers as a dated build) and it is what a bill
  is computed against. When only the request model is known, the row says so
  rather than quietly reporting an alias as a model.
- **Tokens** are `gen_ai.usage.input_tokens` / `output_tokens`, with the
  earlier `prompt_tokens` / `completion_tokens` accepted as aliases. Both
  spellings are in production right now; reading only the current one would
  report a large fraction of real traffic as having used no tokens at all.
- **Truncation is not an error.** `gen_ai.response.finish_reasons` containing
  `length` means the model was cut off at the token ceiling. The call
  succeeded, so counting it as a failure would be wrong — and dropping it would
  hide the single most common cause of an answer that came back nonsense. It
  gets a state of its own, beside the three the rest of the product already
  uses (`errorSpanExpr` / `refusedSpanExpr`, unchanged).

### Reads

Three endpoints, all taking the trace filter vocabulary (window, project,
service, tags) so their numbers reconcile with the Traces screen:

| Endpoint | Answers |
|---|---|
| `GET /api/v1/ai/summary` | the window as one line: calls, tokens in/out, cost, failure and truncation rate, distinct models, and coverage |
| `GET /api/v1/ai/models` | per model: provider, calls, tokens, p50/p95/p99, failed / refused / truncated, cost |
| `GET /api/v1/ai/callers` | per (service, model): calls, tokens, cost — spend with an owner |

No schema and no migration: `SpanAttributes` is a map with a bloom filter on
its keys, which is the same ground the breakdown read already stands on.

### Prices

`ai.prices` is a chart value — a list of `{model, inputPer1MTokens,
outputPer1MTokens}` plus `ai.currency` — and it is **unset by default**. With
no prices the screen reports tokens and says that is what it is reporting.

Matching is exact model id first, then longest declared prefix, so pricing
`gpt-4o` also prices `gpt-4o-2024-08-06`; a row priced by prefix is marked as
such, because the operator should be able to see that a number came from a rule
rather than from an entry they wrote.

There is no pricing API, for the reason the cost module already gave: it would
be the first outbound call in a product whose promise is that nothing leaves
the cluster. There is no bundled price table either, which is the more
tempting mistake — it would be stale within a month and would present a wrong
number with exactly the same confidence as a right one.

### Coverage

Three honesty rails, each guarding a way to be quietly wrong:

- A call with **no usage attributes** is counted as a call, excluded from token
  and cost totals, and reported in a named bucket. Silently treating it as zero
  tokens understates every total on the screen.
- A call whose **model cannot be determined** gets its own row rather than
  being folded into a neighbour.
- A model with **no declared price** is listed with its tokens and no money,
  never with a zero. Zero is a price; absent is not.

### Content — the position

**1. The product never captures content.** Nothing this module adds, and
nothing it switches on, causes a prompt to be recorded. Capture is a setting in
the application's SDK and stays there.

**2. The gateway drops it by default.** A `transform` stage — the processor is
already in the distro, so this costs no new component and no image change —
deletes the content-carrying keys before the exporter sees them:

```yaml
transform/genai:
  error_mode: ignore
  trace_statements:
    - context: span
      statements:
        - 'delete_matching_keys(attributes, "^gen_ai\\.(prompt|completion|input\\.messages|output\\.messages|system_instructions|content)")'
    - context: spanevent
      statements:
        - 'delete_matching_keys(attributes, "^gen_ai\\.(prompt|completion|content|input|output|system_instructions)")'
```

The pattern is anchored so that `gen_ai.usage.prompt_tokens` — the token count
under its older spelling — is never mistaken for a prompt. Deleting it would
delete the number the screen exists to report.

The **span event survives with its attributes emptied**, deliberately. An empty
`gen_ai.content.prompt` event is evidence that the instrumentation is emitting
content, which is precisely what an operator needs to know, carrying none of
it.

`gateway.genai.redactContent` defaults to `true` and is **not gated on the ai
module**. Content arrives whether or not an install runs the screen, so gating
the protection on the screen would protect only the installs that opted into
looking — exactly backwards. An operator who wants content sets it to `false`:
one deliberate decision, written down, instead of a default nobody read.

This is a **behaviour change on upgrade**, and the release note says so. It
applies from the upgrade forward; content already written stays until its TTL
expires it. This AEP adds no backfill delete — a product that silently rewrites
history is a worse promise than one that tells you where to look.

**3. The screen never renders content — and reports it.** The AI screen shows
shapes and counts, never a message. When it detects content keys arriving
despite the default, it says so at the top with the value to set. It is the one
surface in the product positioned to notice, so it notices out loud.

### Module and surface

A new `ai` module, **born OFF**, with no dependency and no schema. Off for the
reason the mesh module gave: most installs call no models, and a nav entry for
something you do not have is what the grouped navigation set out to stop.

It sits under **Signals**, after Profiling. Derived screens in this product are
placed by their *subject*, not their source — Errors is derived from spans and
logs but lives under Operations because it demands attention; Green and Cost
are derived from metrics but live under Infrastructure because their subject is
the fleet. The subject here is what an application asked a model to do, which
is application behaviour: the same layer as its traces and its logs.

The screen carries the summary, the model table, the caller-and-model view, and
a link into the Traces breakdown grouped by `attribute:gen_ai.request.model` —
which already works, and is the general form of this screen. The specific form
earns its place by knowing what the numbers *mean*: that two token columns are
priced differently, that a truncated answer is not a failed one, and that a
missing count is not a zero.

### Alternatives considered

- **Ship a prompt/response viewer behind a switch.** The question to settle
  first is whether content should be stored at all, and building the viewer
  prejudges it — it turns an accident into a feature and rewards the installs
  that never noticed. Once redaction is the default and an operator has opted
  out on purpose, a viewer is a coherent follow-up on top of a decision that
  was actually made.
- **A dedicated `gen_ai` table via a materialized view.** Faster reads, bought
  with a migration, a second copy of rows already stored, and a fork of the
  retention story. The map column and its bloom index are enough at this size.
- **Gate the redaction on the ai module.** One switch, easier to explain, and
  backwards in effect for the reason above.
- **The contrib `redaction` processor** instead of `transform`. It is the
  purpose-built component and it is not in this distro; adding it would grow a
  deliberately minimal gateway image to express statements OTTL already
  expresses.
- **Read OpenInference / vendor-private namespaces too** (`llm.*`,
  `input.value`). Rejected here: those keys are not namespaced away from
  ordinary application attributes, so a redaction rule over them would delete
  fields belonging to services that call no model at all.

## Verification

- **Unit (hub)** — the detection predicate; model fallback response → request
  and the "request only" marking; both token spellings; truncation counted
  apart from error and refusal; price matching exact and by longest prefix;
  each coverage bucket, including a call with no usage that must not become a
  zero.
- **ClickHouse integration** — the per-model and per-caller rollups and their
  quantiles against synthetic `gen_ai` spans that deliberately include one with
  only `request.model`, one on the `prompt_tokens` spelling, one truncated, and
  one with no usage at all.
- **Chart templates** — the redaction stage renders by default, disappears when
  set false, and renders *independently of* `modules.ai.enabled`; the module's
  routes render only with the module on.
- **Gateway (compose e2e)** — the decisive gate. Send a span carrying
  `gen_ai.prompt.0.content` and `gen_ai.usage.prompt_tokens`; assert the content
  is absent from ClickHouse, the token count is present, and a non-AI span
  carrying an attribute called `prompt` is untouched.
- **Playwright** — the model table renders; with no prices configured no
  currency appears anywhere; an install receiving content shows the warning;
  the module-off state shows the gate rather than a hub error.

## Roadmap

- [x] AEP accepted
- [x] Detection, per-model and per-caller reads in storage
- [x] `GET /api/v1/ai/{summary,models,callers}` + `ai` module registration
- [x] Gateway content redaction — default on, ungated by the module
- [x] Prices as chart values, absent by default
- [x] UI: the AI screen, the content warning, the module gate
- [x] Docs site: feature page + setup note (bilingual) — `signals/ai.mdx`, EN + FR

## Follow-ups

- **Agents and tool calls** as a shape rather than a table — `execute_tool`
  spans and `gen_ai.agent.*` describe a graph, and the trace Path view already
  draws one.
- **Prices authored in the UI**, shared with the cost module's rates, once
  either of them needs a second rate table.
- **Token budgets and an alert rule**, the way green already has carbon
  budgets: spend is the number people want a threshold on.
