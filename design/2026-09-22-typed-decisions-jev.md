# AEP: Typed decisions — where a calibrated classifier would fit, and what it would cost the promise

- **Date:** 2026-09-22
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

This is an **evaluation**, not a proposal to ship. It asks one question: if the
product had access to a fast, cheap model that returns a *typed decision with a
calibrated probability* — never text — where in the tree would that decision
be worth more than the rule it replaces, and what would sending the input
cost the promise that nothing leaves the cluster?

The concrete candidate is TypeSafe AI's Jev (released 2026-09-16, early
access). It is not an LLM. A call sends a `state` and a map of typed
questions and gets back, per question, a probability distribution and a
confidence. Three primitives: `noul` (P(statement is true)), `choice` (one of
up to 255 options, per-option probabilities plus confidence) and `score` (2–10
ordered levels). Many questions ride in one call and are evaluated in
isolation. Quoted latency is 70–500 ms end to end; quoted price is
$0.042 per million input tokens, output free. **Hosted only, single region,
Python and JavaScript SDKs, raw REST otherwise. No on-premises option.**

The answer, in one line: five places fit the primitive, three of them clear
the doctrine with an opt-in switch and a structured or already-scrubbed
payload, one is worth a spike today, and none of it belongs in the hub until
that spike has a number.

## Motivation

### Every decision in the tree is a rule

Nothing in avuru-obs learns. Error issues are grouped by a hash over a
five-regex normaliser
([0023](../hub/internal/storage/migrations/0023_error_fingerprint_v2.sql)).
Log severity, when the app did not set it, comes from three OTTL patterns in
the sensor (`transform/log_severity`,
[sensor-config.yaml](../deploy/helm/avuruobs/templates/sensor-config.yaml)).
Alerting is a three-condition state machine with a dwell timer
([evaluator.go](../hub/internal/alerting/evaluator.go)). Service roles are
glob tables; health is thresholds; the mesh posture is a truth table. That is
a feature — a rule is explainable, free, and runs inside the cluster — right
up to the point where the rule's known failure mode is documented in its own
comment and left there because the next step needs judgement, not a sixth
regex.

Three such comments exist. The fingerprint AEP records "fifteen issues for one
retry loop". The severity transform records that any format outside its three
shapes lands at severity 0 and vanishes from both the log floor and error
issues. The AI-observability AEP rejected reading vendor `llm.*` attributes
*because a rule cannot tell them from ordinary attributes*
([AEP](2026-08-27-ai-observability.md#alternatives-considered)).

### The doctrine, restated

The hub makes no outbound call. It is why prices are declared rather than
fetched ([AEP](2026-08-26-cost-and-waste.md)), why root-cause summaries are
still out of the roadmap ([ROADMAP](../ROADMAP.md#beyond-v017-directional)),
and why the MCP server's switch "has to be theirs to throw"
([modules.go](../hub/internal/modules/modules.go)). A hosted-only classifier
inherits all of that. So the useful question is not "is Jev good" but "which
inputs are already safe to send, and which decisions are worth an operator
opting in for".

### Goals

- Rank the decision points by fit for a calibrated classifier *and* by what
  the payload would disclose.
- Name the one worth measuring, and the numbers that would make it a go.
- Record why the others wait, so the next person does not re-derive it.

### Non-goals

- Shipping any call from the hub, the gateway or the sensor.
- Root-cause *summaries*. Jev cannot generate text; that question stays where
  the roadmap left it.
- Replacing rules that are already right and free (service roles, health
  thresholds, the posture table).

## Solution

### The map

Ranked on two axes: fit for the primitive, and what leaves the cluster.

| # | Decision | Today | Primitive | Payload | Verdict |
|---|---|---|---|---|---|
| A1 | **Severity of a log line the sensor left at 0** | Three OTTL patterns, sensor side | `choice` over the six OTel buckets | the log body, alone | **Spike now** |
| A2 | **Are these two error issues the same error?** | `cityHash64` over a normalised message | `noul` per candidate pair | normalised message + top frames (timestamps, UUIDs, hex already scrubbed) | Next, if A1 clears |
| A3 | **Is this transition an incident, and is it a symptom of one already firing?** | `down`/`degraded`/`not-healthy` + dwell | `score` urgency, `noul` symptom-of-upstream | structured metadata: service, tier, reason, rate delta, critical-dep status, firing neighbours — no logs | The decision half of "The incident", without the summary |
| B1 | **Does this attribute *key set* describe a model call?** | `gen_ai.*` presence only; vendor namespaces rejected | `noul` over keys | attribute **names**, never values | Small and clean; keys disclose schema, not content |
| B2 | **Does this log body carry a secret or personal data?** | nothing; MCP deliberately does not redact bodies | `noul` | the body — the thing being protected | Only as a pre-flight on MCP egress the operator already opted into |
| C | Service role, health verdict, log clustering, MCP ranking | rules / thresholds / none / forbidden by AEP | — | — | Not worth an outbound dependency; the model is weak at numbers and dates; clustering wants embeddings, not decisions |

### Why A1 is the spike

- **The label set is closed.** Six buckets, no free text, no counting, no
  arithmetic — the shape the model is built for.
- **Calibration is directly actionable.** Below a confidence floor, keep 0.
  The rule already does this implicitly by matching nothing; the model does
  it explicitly with a number an operator can set.
- **Ground truth is free.** Every line the OTTL cascade *did* label is a
  labelled example; every line it left at 0 is the target population. No
  annotation, no synthetic data.
- **It is a backfill, not a hop.** The sensor is a DaemonSet with no network
  budget for this. The candidate design is a hub-side batch over rows with
  `SeverityNumber = 0`, opt-in, off by default, throttled. Ingestion never
  waits on it; the wedge is untouched.
- **What leaves is a log body.** That is the honest cost, and it is why the
  spike runs from a laptop against the compose stack and never from the hub.

### What the spike measures

`tools/jev-eval/` — stdlib Python, no SDK, no dependency. It samples labelled
and unlabelled bodies from ClickHouse, asks one `choice` question per body
(optionally packed several per call), and reports:

- accuracy against the cascade's labels, at confidence floors 0.5 / 0.7 / 0.9;
- coverage recovered on the severity-0 set at each floor;
- p95 latency per call; input tokens per line; dollars per million lines.

It replays recorded responses when no key is present, so the harness is
testable before early access is granted.

### Go / no-go

| Measure | Go |
|---|---|
| Accuracy vs cascade labels at confidence ≥ 0.7 | ≥ 0.95 |
| Severity-0 lines recovered at that floor | ≥ 30 % |
| p95 latency per call | < 500 ms |
| Cost at observed tokens | < $2 per million lines (about 50 input tokens a line at the quoted price) |

A go does not ship a call. It opens a second AEP for a born-OFF module with
the MCP precedent's consent language: batch only, structured or scrubbed
payloads only, raw REST from Go, and a release note that says in as many
words what leaves.

### Alternatives considered

- **An in-cluster classifier** (a small fine-tuned model in the hub image).
  It would dissolve the doctrine objection entirely, and it is the right
  answer if A1 clears and the vendor never offers on-premises inference. It
  costs a training set — which the spike produces as a by-product — and an
  image that grows for every install, including the ones that never turn it
  on. Deferred until there is a number worth training toward.
- **A sixth regex.** Cheaper, inside the cluster, and exactly the treadmill
  the severity transform's comment describes. It does not address the
  population the spike targets: formats nobody has written a pattern for yet.
- **An LLM with a JSON schema.** Two orders of magnitude slower and dearer for
  the same closed label set, and the roadmap's objection to it was never the
  price.
- **Do nothing.** The default, and the correct one until the spike has run.

## Verification

- `python3 -m unittest discover -s tools/jev-eval` passes with no network and
  no key (recorded fixtures).
- One live run against the compose stack with `TYPESAFE_API_KEY` set; the
  report is committed next to this AEP as `2026-09-22-typed-decisions-jev-report.md`
  with the numbers filled in, and the status of this AEP moves to Accepted or
  Rejected on those numbers.
- Nothing under `hub/`, `gateway/`, `sensor/` or `deploy/` changes in this AEP.

## Roadmap

- [ ] AEP reviewed as an evaluation
- [ ] Early-access key obtained (waitlist as of 2026-09-22)
- [ ] Live run; report committed; go/no-go recorded
- [ ] If go: second AEP for a born-OFF `typed-decisions` module (A1 backfill first, A2 second)
- [ ] If no-go: note here, keep the harness for the next candidate

## References

- Model and API: typesafe.ai, docs.typesafe.ai (`api.md`, `primitives.md`,
  `confidence.md`, `model-jaggedness/jev-1.13.md`), retrieved 2026-09-22.
- Known limits recorded by the vendor: counting, arithmetic, date ordering,
  hex and low-level encodings, accuracy falling with irrelevant state,
  literal reading of instructions, no adversarial hardening of the state.
  Each one shaped a row of the map above.
- Prior art in this tree: [AI observability](2026-08-27-ai-observability.md),
  [MCP server](2026-09-01-mcp-server.md),
  [error tracking](2026-07-16-error-tracking.md).
