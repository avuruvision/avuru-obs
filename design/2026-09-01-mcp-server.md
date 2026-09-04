# AEP: A Model Context Protocol server — the estate an agent can read

- **Date:** 2026-09-01
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

v0.11 taught the product to read the model calls applications were already
sending. v0.12 turns that report into budgets you can be paged on and turns an
agent's tool calls into a shape you can look at. Both point the same way: this
product knows a great deal about agents. Nothing yet lets an agent know
anything about this product.

This proposes a **Model Context Protocol server, served by the hub** — six
read-only tools over the spans, logs, issues and health the wedge already
stores, reachable from a claude.ai connector, from Claude Code, or from any
other MCP client. Its job is one job: **investigate an incident**. It adds no
collection, no schema, no image and no chart component; it is born OFF like
`ai`, `cost` and `green`; and when an operator turns it on, the release says in
as many words what leaves the installation.

## Motivation

### The client that is missing

The Hub API is the client-agnostic contract and the web app is one client of
it. The CLI is a second, the Grafana data source a third
([AEP](2026-07-27-clients-grafana-cli.md)). All three render for a person: a
browser, a terminal, a dashboard.

An engineer debugging a failing service increasingly has an agent beside them,
and that agent is handed the codebase, the logs it can `cat`, and nothing else.
The observability estate — the one system that knows what the service actually
does in production, who calls it, what it depends on, and what changed — is
reachable only by a human reading a screen and retyping what they saw. The
gap is not analysis. It is access.

### Why this is small

Because the API handlers are already thin. `handleServices` is the whole
pattern:

```go
tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
services, err := store.ListServices(r.Context(), storage.ServiceQuery{…})
resp := servicesResponse{…toServiceDTO(s, window)…}
```

Authorization, project scope, the query and the DTO are all there. An MCP tool
is that function without the `http.ResponseWriter`. What this AEP adds is a
protocol envelope, six tool definitions, and a decision about what leaves the
cluster — not a second read path, and emphatically not a second set of SQL.

### The doctrine question, stated plainly

This product's promise is that nothing leaves the cluster. It is why there is
no pricing API ([AEP](2026-08-26-cost-and-waste.md)), why prices are declared
rather than fetched ([AEP](2026-08-27-ai-observability.md)), and why
root-cause summaries from a model are still out.

An MCP server does not break that promise: the product still makes no outbound
call. But it would be dishonest to stop there. An operator who connects a model
provider to this server is exporting traces and **log bodies** by their own
hand, and log bodies are where user data lives on the installs that have any.

v0.11 met this exact class of problem and named it: user text was being stored
and rendered because an SDK captured it, "no feature put it there and no
feature would have shown you." The lesson taken from that was not *redact
everything* — it was *say what happens, and make the operator choose*. Prompt
content is dropped at the gateway by default because nobody asked for it to be
kept. Here, the content is the point of the request: an agent diagnosing a
failure needs the log line, and the log line we would redact is invariably the
useful one. So the answer is not redaction. It is an opt-in module, a sentence
that does not hide, and an audit trail.

Ties to the [wedge](../AGENTS.md): nothing collected changes, no chart component
is added, the time-to-value gate is untouched. Ties to the
[locked decisions](../agent_docs/architecture.md#locked-decisions-and-rationale):
the hub reads via SQL through `storage.Store`, the module framework gates the
surface, and the
[enterprise seam](../agent_docs/architecture.md#enterprise-seam-do-not-bypass)
is used exactly as it stands — a personal API token resolves its owner's live
permissions, and `projectTenants` decides what tenant a read spans. No parallel
authorization is introduced.

### Goals

- **One agent, one incident.** Enough to diagnose a failing service without
  opening a browser.
- **Reachable from the clients people actually use** — a claude.ai connector
  and Claude Code both, which means a remote HTTP transport.
- **The identity model unchanged.** A token sees exactly what its owner sees.
- **Nothing new collected, nothing new deployed.**
- **Say what leaves.**

### Non-goals

- **Writes of any kind** — acknowledging an alert, editing a rate, changing a
  group. A read-only first server cannot break a production estate, and the
  write surface deserves its own decision about what an agent may do
  unattended.
- **A stdio transport.** It would not reach a claude.ai connector, which is
  half the point, and the HTTP server makes it redundant for the rest.
- **MCP resources and prompts.** Worth having once we know how agents actually
  use the tools; guessing now would freeze the wrong shape.
- **Scoped or per-tool tokens.** Today a token resolves its owner's *live*
  permissions with no parallel authorization to keep in sync
  ([AEP](2026-08-13-api-tokens.md)); introducing scopes would break that rule
  for one client.
- **Redacting log bodies** — see above.
- **Doing the diagnosis in the server.** The tools return facts. A tool named
  `why_is_service_failing()` would freeze our current theory of failure into a
  contract and leave an agent unable to ask anything we did not anticipate.

## Solution

### Where it lives

A new package `hub/internal/mcp`, mounted by the hub at `POST /mcp` inside the
module-gated block of `hub/internal/api/router.go`:

```go
mux.Handle("POST /mcp", a.secured(auth.RoleViewer, a.handleMCP))
```

Going through `secured` is the whole design of the authorization half: identity
resolution, role check and project scope are the ones every other route uses,
resolved before a single tool runs.

| File | Role |
|---|---|
| `protocol.go` | JSON-RPC 2.0 envelope, `initialize`, `tools/list`, `tools/call`, error codes |
| `tools.go` | The six tool definitions, the registry and the module gate |
| `store.go` | The narrow slice of `storage.Store` the tools read |
| `args.go` | Window translation, row bounds, service resolution and near matches |
| `context.go` | `service_context` — the composite entry point |
| `signals.go` | `list_services`, `search_traces`, `get_trace`, `search_logs`, `list_error_issues` |
| `audit.go` | The per-call structured log line |

Tools call the same `storage.Store` methods the REST handlers call, they do not
call the REST API over the network, and they do not write SQL of their own —
that is what keeps the two surfaces from drifting into disagreeing about the
same number, the lesson service groups taught when the alerting evaluator
turned out to be reading different config than the API served. They build their
own payload shapes rather than reusing the wire DTOs: those are unexported in
package `api`, and what an agent needs from a row is not what a chart needs
from it. The shared thing is the READ, which is the thing that can drift.

### The six tools

| Tool | Arguments | Returns |
|---|---|---|
| `service_context` | `service`, `window` | Health, req/s, error rate, p50/p95/p99, top error issues, callers and dependencies with rate/error-rate/p95, active alerts — **in one call** |
| `list_services` | `unhealthy_only`, `window` | The inventory, for finding the service when the agent has only a symptom |
| `search_traces` | `service`, `operation`, `status`, `min_duration_ms`, `window` | Matching requests, the same filters the trace screen exposes |
| `get_trace` | `trace_id` | One request: a compact span tree (id, parent, service, operation, duration, status) plus a per-service self-time rollup |
| `search_logs` | `service`, `level`, `query`, `trace_id`, `window` | Log lines, correlatable by `trace_id` |
| `list_error_issues` | `service`, `window` | Deduplicated issues, not raw exceptions |

Every tool takes a relative `window` (`15m`, `1h`, `24h`; default `1h`) and
optionally absolute `start`/`end` in RFC3339. The API itself is absolute-only —
`parseTimeRange` reads `start`/`end` and defaults the rest — but an agent
reasons in "the last twenty minutes", and making it compute two timestamps to
ask that is friction with no upside. The relative form is translated at the
tool boundary and the absolute pair stays available for the agent that has one.

`get_trace` needs one piece of new arithmetic: the per-service **self time** of
a trace. Today that weighting lives in the browser
(`ui/src/components/traces/views/trace-path.tsx`), which is where v0.11 put the
Path view, so the hub cannot hand it over. This AEP computes it in the hub for
the tool and **leaves the Path view alone**, which means the same weighting has
two implementations for a while. That is a real cost and it is taken
deliberately: unifying them means touching a shipped screen, which would put a
UI regression in the blast radius of a change that otherwise adds nothing to
any existing surface. The follow-up — the UI reading the hub's number — is a
separate, safer change.

`service_context` exists because the first call is the one an agent gets wrong.
Given a bare tool per endpoint, a model opens an investigation by guessing, and
spends five round trips assembling what one query already knows. Given a
composite, it starts with the picture a human starts with — and the five
granular tools are there the moment it needs to go deeper.

### Three rules that matter as much as the list

- **An unknown service is an error, not an empty list.** A misspelled name that
  returns `[]` reads to a model as *this service is dead*, and it will say so
  with confidence. The tool returns a structured error naming the closest
  matches instead.
- **A tool whose module is off does not appear in `tools/list`.** Logs and
  error issues belong to modules an install may not run. This is the
  capabilities pattern the sidebar already uses to hide a screen rather than
  render a gap — never a tool that always fails.
- **Every response is bounded and says so.** Row limits with an explicit
  truncation note, because a model handed a top-20 with no marker will reason
  about it as the whole estate. It is the same honesty as v0.11's "reported no
  usage" bucket: name the gap rather than let a number imply something false.

### Authorization, in two steps

**Step 1 — Bearer.** `/mcp` accepts `Authorization: Bearer avurut_…`, the
personal API token that already exists. Claude Code, the CLI and any client
that can set a header work immediately, on the permission model that has been
in production since v0.5. This step is shippable and useful on its own.

**Step 2 — OAuth, in its own PR.** A claude.ai connector expects an OAuth 2.1
flow: protected-resource and authorization-server metadata, dynamic client
registration, authorization code with PKCE, short-lived access tokens. The hub
already runs an authorization-code + PKCE flow as an OIDC *client*; here it
acts as the authorization server, with the consent screen backed by the
existing login session. It is the expensive half, it carries its own security
review, and nothing in step 1 waits on it.

### What leaves, and how we say it

The server returns what the token's owner can already see in the UI, log bodies
included. Three obligations ship with it, and none is optional:

1. **The module is born OFF**, like `ai`, `cost` and `green`.
2. **One explicit sentence** in `values.yaml`, the docs and the release notes:
   turning this on means traces and log bodies leave the installation, to the
   model provider the operator chose.
3. **Every tool call is logged** with the token owner, the tool, the arguments
   and the row count — never the content returned. An operator can answer
   "what did the agent read, and whose token did it use" from the hub's own
   logs.

### Alternatives considered

- **stdio, via an `avuruobs mcp` subcommand.** Cheapest by far, nothing
  deployed, no network surface — and it cannot serve a claude.ai connector,
  which only speaks to remote servers. It would also split the tool
  implementation across two repositories' worth of client code the first time
  the API changed.
- **A dedicated `avuru-obs-mcp` container in the chart.** Cleanest separation,
  and one more image to build, scan, sign and publish every release for a
  component most installs will never enable. The hub already serves an
  authenticated HTTP API; this is another handler on it.
- **One tool per API endpoint.** Familiar and perfectly composable, at the cost
  of five to eight round trips per investigation and a first call chosen by
  guesswork.
- **Question-shaped tools** (`why_is_service_failing`, `what_changed`). Fewest
  round trips, and it hard-codes our diagnosis into the server: an agent can
  then only ask the questions we thought of, and every new question is a
  release.
- **Redaction by default**, on the pattern of `gateway.genai.redactContent`.
  Safest-looking, and it makes the tool useless for the job it exists to do
  while creating a second redaction doctrine to keep in sync with the first.
- **Scoped MCP tokens** ("metrics only" vs "content"). The finest control, and
  it breaks the API-token rule that there is no parallel authorization to
  maintain.

## Verification

- **A test per tool** over the existing fake store
  (`hub/internal/storage/storagetest/fake.go`), table-driven — including the
  unknown-service case asserting a structured error with near matches, and a
  truncation case asserting the marker.
- **A protocol test over HTTP**: `initialize` → `tools/list` → `tools/call`,
  asserting the JSON-RPC envelope, that an unauthenticated call is refused,
  that a Viewer sees exactly what a Viewer sees, and that a tool belonging to a
  disabled module is **absent from `tools/list`** rather than failing on call.
- **An e2e case in the Go suite**: seeded spans, then `service_context` returns
  the seeded service with the error rate the seed implies.
- **`deploy/helm/template-test.sh`**: with the module off, nothing renders.
- **The wedge is untouched** — no chart component changes, so `e2e-helm` and
  the time-to-value gate must be unaffected. That they are is the check.
- **Manual**: `claude mcp add --transport http …`, then a real investigation
  against the demo stack.

## Roadmap

- [x] AEP accepted
- [x] Module registered (born OFF) + protocol skeleton behind Bearer
- [x] The six tools, bounded responses, and the audit line
- [x] e2e case + chart template test
- [x] `service_context` recovers mesh-hidden dependencies — and not as a second
      set of semantics: the merge moved out of the service-map handler into
      `hub/internal/topology`, so both surfaces call the same functions
      ([AEP](2026-08-25-transport-hop-collapse.md)).
- [x] The Path view reads the hub's self-time instead of computing its own —
      one rollup in `hub/internal/tracestats`, on the REST trace response the
      view already fetched. Unifying them surfaced a real defect: `get_trace`
      counted only a raw `error` status, so an auto-instrumented 5xx was
      reported as a success.
- [x] OAuth 2.1 for claude.ai connectors — discovery metadata, RFC 7591
      registration, authorization code with PKCE (S256 only), rotating
      refresh tokens, opaque audience-bound access tokens, and a consent
      screen that states what leaves the installation. Off by default and
      behind its own switch, separate from the module's.
- [x] Docs: bilingual changelog, feature-status matrix, API reference
- [x] README: the capability line
