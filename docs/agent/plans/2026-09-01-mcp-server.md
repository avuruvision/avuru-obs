# MCP Server (step 1 — Bearer, read-only) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve a Model Context Protocol server from the hub at `POST /mcp`, gated by a new born-OFF `mcp` module, exposing six read-only tools over the traces, logs, issues and health already stored — so an agent can investigate an incident without a browser.

**Architecture:** A new `hub/internal/mcp` package owns the JSON-RPC 2.0 envelope and the six tools. It owns no SQL and no authorization: `hub/internal/api` mounts the route behind the same `a.secured(auth.RoleViewer, …)` every other route uses, resolves the identity and the tenant set there, and hands them down as `Server` fields. Tools read through the same `storage.Store` methods the REST handlers read, so the two surfaces cannot drift into disagreeing about the same number.

**Tech Stack:** Go 1.23+, stdlib `net/http` (Go 1.22 mux), `encoding/json`, `log/slog`. No new dependency, no new image, no new chart component, no schema.

**Spec:** `design/2026-09-01-mcp-server.md` (AEP, Draft).

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `hub/internal/modules/modules.go` | Modify | Add `MCP Name = "mcp"` to the registry, born OFF |
| `hub/internal/modules/modules_test.go` | Modify | The registry-order assertions list every name |
| `ui/src/lib/api-types.ts` | Modify | `ModuleName` union mirrors the Go registry |
| `deploy/helm/avuruobs/values.yaml` | Modify | `modules.mcp.enabled: false` + the sentence about what leaves |
| `deploy/helm/avuruobs/templates/_helpers.tpl` | Modify | `mcp` joins the `AVURUOBS_MODULES` CSV |
| `deploy/helm/template-test.sh` | Modify | Off by default; on when asked |
| `hub/internal/mcp/protocol.go` | Create | JSON-RPC 2.0 envelope, `initialize`/`ping`/`tools/list`/`tools/call` dispatch |
| `hub/internal/mcp/protocol_test.go` | Create | Dispatch, notifications, malformed bodies |
| `hub/internal/mcp/tools.go` | Create | The six definitions, their schemas, and the module gate |
| `hub/internal/mcp/tools_test.go` | Create | A disabled module's tool is absent from `tools/list` |
| `hub/internal/mcp/args.go` | Create | Window translation, row limits, service resolution + near matches |
| `hub/internal/mcp/args_test.go` | Create | Windows, clamping, the unknown-service error |
| `hub/internal/mcp/signals.go` | Create | `list_services`, `search_traces`, `get_trace`, `search_logs`, `list_error_issues` |
| `hub/internal/mcp/signals_test.go` | Create | One test per tool over `storagetest.Fake` |
| `hub/internal/mcp/context.go` | Create | `service_context` — the composite entry point |
| `hub/internal/mcp/context_test.go` | Create | Callers/dependencies/issues/alerts assembled in one call |
| `hub/internal/mcp/audit.go` | Create | The per-call structured log line |
| `hub/internal/mcp/audit_test.go` | Create | The line names the actor, tool, args and row count — never content |
| `hub/internal/api/mcp.go` | Create | `handleMCP`: resolve store + tenants, build the Server, write the reply |
| `hub/internal/api/mcp_test.go` | Create | 404 without the module, 401 without a credential, 200 with a Bearer |
| `hub/internal/api/router.go` | Modify | Mount `POST /mcp` in a module-gated block |
| `e2e/mcp_test.go` | Create | Seeded spans → `service_context` reports them |
| `CHANGELOG.md` | Modify | Unreleased entry |
| `design/2026-09-01-mcp-server.md` | Modify | Tick the roadmap boxes; correct the file table and the DTO sentence |

The package split follows the AEP with one deviation, made deliberately: the AEP's five-file table has no home for argument decoding, and service resolution is needed by five of the six tools. `args.go` is that home, and Task 13 corrects the AEP table rather than leaving it describing a layout that does not exist.

---

### Task 1: Register the `mcp` module, born OFF

**Files:**
- Modify: `hub/internal/modules/modules.go`
- Modify: `hub/internal/modules/modules_test.go`
- Modify: `ui/src/lib/api-types.ts`

- [ ] **Step 1: Write the failing test**

In `hub/internal/modules/modules_test.go`, add `"mcp"` to the end of both all-module expectations inside `TestParse` and add one case proving it is opt-in. Replace the first two table rows:

```go
		{"empty means all", "", []string{"core", "logs", "infra-metrics", "profiling", "error-tracking", "service-health", "alerting", "mesh", "green", "cost", "ai", "mcp"}, false},
		{"whitespace means all", "  ", []string{"core", "logs", "infra-metrics", "profiling", "error-tracking", "service-health", "alerting", "mesh", "green", "cost", "ai", "mcp"}, false},
```

and add this row after the `cost` rows:

```go
		{"mcp is opt-in and depends on nothing", "mcp", []string{"core", "mcp"}, false},
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd hub && go test ./internal/modules/... -run TestParse -v
```

Expected: FAIL — `Parse("") = [core logs infra-metrics profiling error-tracking service-health alerting mesh green cost ai], want [... ai mcp]`, and `Parse("mcp")` errors with `unknown module "mcp"`.

- [ ] **Step 3: Add the module to the registry**

In `hub/internal/modules/modules.go`, add the constant immediately after `AI` inside the same `const` block:

```go
	// MCP serves a Model Context Protocol server at POST /mcp: the read-only
	// tools an agent uses to investigate an incident, over the spans, logs,
	// issues and health this install already stores. It owns no schema, adds
	// no collection, deploys no component and depends on no other module —
	// the tools of a module that is off are simply absent from tools/list.
	//
	// Born OFF, and for a reason the other opt-off modules do not share: this
	// is the one surface whose whole purpose is to hand telemetry to something
	// outside the cluster. The product still makes no outbound call — the
	// operator's agent pulls — but an operator who connects a model provider
	// to this server is exporting traces and log bodies by their own hand, so
	// the switch has to be theirs to throw.
	// See design/2026-09-01-mcp-server.md.
	MCP Name = "mcp"
```

and append it to `All`:

```go
// All lists every known module in registry (display) order.
var All = []Name{Core, Logs, InfraMetrics, Profiling, ErrorTracking, ServiceHealth, Alerting, Mesh, Green, Cost, AI, MCP}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd hub && go test ./internal/modules/... -v
```

Expected: PASS (all of `TestParse`, `TestParseGreenDependencyError`, `TestEnabled`).

- [ ] **Step 5: Keep the UI's mirror of the registry in sync**

In `ui/src/lib/api-types.ts`, extend the union — the comment above it says these names mirror the Go registry, and a hub with the module on will send `"mcp"` in `GET /api/v1/capabilities`:

```ts
export type ModuleName =
  | "core"
  | "logs"
  | "infra-metrics"
  | "profiling"
  | "error-tracking"
  | "service-health"
  | "alerting"
  | "mesh"
  | "green"
  | "cost"
  | "ai"
  | "mcp";
```

No UI surface follows: `mcp` lights up no nav entry, because there is no screen to show. It is in the union so `capabilities.modules` still type-checks against what the hub sends.

- [ ] **Step 6: Verify the UI still builds**

```bash
cd ui && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add hub/internal/modules/modules.go hub/internal/modules/modules_test.go ui/src/lib/api-types.ts
git commit -m "feat(hub): register the mcp module, born off

It gates one route and nothing else — no schema, no collection, no
component. Off by default because it is the one surface whose purpose is
to hand telemetry to something outside the cluster, so the switch has to
be the operator's to throw."
```

---

### Task 2: The chart switch and what it says

**Files:**
- Modify: `deploy/helm/avuruobs/values.yaml:267-268` (after the `ai:` block)
- Modify: `deploy/helm/avuruobs/templates/_helpers.tpl:92` (the `activeModules` helper)
- Modify: `deploy/helm/template-test.sh` (append before the final `echo "ALL TEMPLATE ASSERTIONS PASSED"`)

- [ ] **Step 1: Write the failing assertions**

Append to `deploy/helm/template-test.sh`, immediately **before** the final `echo "ALL TEMPLATE ASSERTIONS PASSED"` line:

```bash
echo "== mcp: born opt-off -> no module entry, no route"
out="$(render)"
grep -A1 'name: AVURUOBS_MODULES' <<<"$out" | grep -qE ',mcp[,"]' && fail "mcp in AVURUOBS_MODULES without opt-in"
ok "no module entry by default"

echo "== mcp: module on -> module entry, and still no new component"
out="$(render --set modules.mcp.enabled=true)"
grep -q 'core,logs,infra-metrics,profiling,error-tracking,service-health,alerting,mcp' <<<"$out" \
  || fail "mcp missing from AVURUOBS_MODULES"
# The whole claim of this feature: it is a handler on the hub, not a
# deployable. If turning it on ever renders a Deployment, the time-to-value
# gate is no longer measuring the same install.
before="$(render | grep -c '^kind: Deployment' || true)"
after="$(grep -c '^kind: Deployment' <<<"$out" || true)"
[ "$before" = "$after" ] || fail "mcp added a Deployment ($before -> $after)"
ok "module entry renders; component count unchanged"
```

- [ ] **Step 2: Run the assertions to verify they fail**

```bash
cd deploy/helm && ./template-test.sh
```

Expected: FAIL — `FAIL: mcp missing from AVURUOBS_MODULES` (the second block; the first passes vacuously because nothing renders `mcp` yet, and `helm template --set modules.mcp.enabled=true` on a chart with no such value simply sets an unused key).

- [ ] **Step 3: Add the value**

In `deploy/helm/avuruobs/values.yaml`, insert immediately after the `ai:` block's `enabled: false` (currently line 268) and before the blank line preceding `# Mesh control plane.`:

```yaml
  # MCP: a Model Context Protocol server at POST /mcp, so an agent — a
  # claude.ai connector, Claude Code, any MCP client — can read this estate to
  # investigate an incident: service health, dependencies, traces, logs and
  # error issues. No new collection, no schema, no extra container: it is one
  # handler on the hub, authenticated with a personal API token, and it returns
  # exactly what that token's owner can already see in the UI.
  #
  # READ THIS BEFORE TURNING IT ON. Everything above still holds — this product
  # makes no outbound call of its own. But an agent you connect to this server
  # pulls traces and LOG BODIES out of the cluster and into whichever model
  # provider you pointed it at. Log bodies are where user data lives on the
  # installs that have any. That is the trade this switch makes, which is why
  # it is off by default and why every tool call is logged with the token
  # owner, the tool, its arguments and the row count.
  # See design/2026-09-01-mcp-server.md.
  mcp:
    enabled: false
```

- [ ] **Step 4: Add it to the module CSV**

In `deploy/helm/avuruobs/templates/_helpers.tpl`, inside `avuruobs.activeModules`, add the line after the `ai` line:

```
{{- if .Values.modules.mcp.enabled -}}{{- $mods = append $mods "mcp" -}}{{- end -}}
```

- [ ] **Step 5: Run the assertions to verify they pass**

```bash
cd deploy/helm && ./template-test.sh
```

Expected: `ok: no module entry by default`, `ok: module entry renders; component count unchanged`, then `ALL TEMPLATE ASSERTIONS PASSED`.

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/avuruobs/values.yaml deploy/helm/avuruobs/templates/_helpers.tpl deploy/helm/template-test.sh
git commit -m "feat(chart): modules.mcp, off by default, and the sentence that goes with it

The switch says in as many words what turning it on means: traces and log
bodies leave the install, to whichever model provider the operator chose.
A render-time assertion pins the other half — no new component, so the
time-to-value gate keeps measuring the same install."
```

---

### Task 3: The JSON-RPC envelope

**Files:**
- Create: `hub/internal/mcp/protocol.go`
- Create: `hub/internal/mcp/protocol_test.go`

- [ ] **Step 1: Write the failing test**

Create `hub/internal/mcp/protocol_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

// call runs one request body through a server with every module active and
// returns the decoded reply. A notification (no reply) returns nil.
func call(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	out, err := s.Handle(context.Background(), []byte(body))
	if err != nil {
		t.Fatalf("Handle(%s): %v", body, err)
	}
	if out == nil {
		return nil
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding reply %s: %v", out, err)
	}
	return got
}

func testServer() *Server {
	return &Server{Modules: modules.AllSet(), Tenant: "default", Tenants: []string{"default"}, Version: "test"}
}

func TestInitialize(t *testing.T) {
	got := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	res, ok := got["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", got)
	}
	if res["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %q", res["protocolVersion"], ProtocolVersion)
	}
	caps, ok := res["capabilities"].(map[string]any)
	if !ok || caps["tools"] == nil {
		t.Errorf("server must advertise the tools capability: %v", res)
	}
	info, ok := res["serverInfo"].(map[string]any)
	if !ok || info["name"] != ServerName || info["version"] != "test" {
		t.Errorf("serverInfo = %v, want name=%q version=test", info, ServerName)
	}
}

// A notification carries no id and takes no reply at all. Answering one is a
// protocol violation that some clients treat as a fatal desync, so this is not
// a cosmetic assertion.
func TestNotificationGetsNoReply(t *testing.T) {
	if got := call(t, testServer(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`); got != nil {
		t.Errorf("notification answered with %v, want no reply", got)
	}
}

func TestProtocolErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		code float64 // JSON numbers decode as float64
	}{
		{"unparseable body", `{not json`, codeParse},
		{"wrong jsonrpc version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, codeInvalidRequest},
		{"missing method", `{"jsonrpc":"2.0","id":1}`, codeInvalidRequest},
		{"batch is not supported", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, codeInvalidRequest},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, codeMethodNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := call(t, testServer(), tt.body)
			e, ok := got["error"].(map[string]any)
			if !ok {
				t.Fatalf("no error object in %v", got)
			}
			if e["code"] != tt.code {
				t.Errorf("code = %v, want %v", e["code"], tt.code)
			}
			if got["result"] != nil {
				t.Errorf("error reply also carried a result: %v", got)
			}
		})
	}
}

func TestPing(t *testing.T) {
	got := call(t, testServer(), `{"jsonrpc":"2.0","id":"a","method":"ping"}`)
	if got["error"] != nil {
		t.Fatalf("ping errored: %v", got)
	}
	if got["id"] != "a" {
		t.Errorf("id = %v, want the string \"a\" echoed back unchanged", got["id"])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd hub && go test ./internal/mcp/...
```

Expected: FAIL — `no required module provides package .../hub/internal/mcp` (the package does not exist yet).

- [ ] **Step 3: Write the protocol**

Create `hub/internal/mcp/protocol.go`:

```go
// Package mcp serves the Model Context Protocol over the hub's HTTP API: the
// read-only tools an agent uses to investigate an incident, over the spans,
// logs, issues and health this install already stores.
// See design/2026-09-01-mcp-server.md.
//
// The package owns the protocol envelope and the tools. It owns no SQL and no
// authorization, and that is deliberate on both counts: hub/internal/api mounts
// POST /mcp behind the same secured() every other route uses and resolves the
// identity and the tenant set there, and every tool reads through the same
// storage.Store methods the REST handlers read. A second read path is how two
// surfaces end up disagreeing about the same number.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

// ProtocolVersion is the MCP revision this server implements, and it is
// reported rather than echoed: a client that asked for another revision must
// see what it is actually talking to and decide, not be told what it wanted to
// hear.
const ProtocolVersion = "2025-06-18"

// ServerName identifies this server to a client in `initialize`.
const ServerName = "avuru-obs"

// The JSON-RPC 2.0 error codes this server raises.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// request is one JSON-RPC 2.0 call. A missing ID makes it a notification,
// which takes no reply at all.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is one JSON-RPC 2.0 reply; exactly one of Result/Error is set.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server answers one request. It is built per HTTP request by the API handler,
// which has already decided who the caller is, that they hold Viewer, and
// which tenants their read spans — this type never makes any of those calls.
type Server struct {
	Store   Store
	Modules modules.Set
	Tenant  string
	Tenants []string
	Version string // hub build version, reported by initialize
	Actor   string // who the audit line names (the token owner)
	// Now is the clock, injectable so a window test is not a race. nil → time.Now.
	Now func() time.Time
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// Handle runs one JSON-RPC request and returns the reply body, or nil for a
// notification. The error return is reserved for a failure to MARSHAL a reply,
// which is a bug here rather than anything the client did: everything the
// client can get wrong comes back as a JSON-RPC error object, because a caller
// speaking the protocol has to be answered inside it.
func (s *Server) Handle(ctx context.Context, body []byte) ([]byte, error) {
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return marshalReply(response{JSONRPC: "2.0", Error: &rpcError{codeParse, "request body is not valid JSON"}})
	}
	switch {
	case req.JSONRPC != "2.0":
		return s.fail(req, codeInvalidRequest, `"jsonrpc" must be "2.0"`)
	case req.Method == "":
		return s.fail(req, codeInvalidRequest, `"method" is required`)
	}
	result, rerr := s.dispatch(ctx, req)
	if len(req.ID) == 0 {
		// A notification. Errors on one are still not answered — there is no
		// id to answer to — so the only place they can be reported is the log.
		return nil, nil
	}
	if rerr != nil {
		return marshalReply(response{JSONRPC: "2.0", ID: req.ID, Error: rerr})
	}
	return marshalReply(response{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (s *Server) fail(req request, code int, msg string) ([]byte, error) {
	if len(req.ID) == 0 {
		return nil, nil
	}
	return marshalReply(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{code, msg}})
}

func (s *Server) dispatch(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return initializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities:    capabilities{Tools: map[string]any{}},
			ServerInfo:      serverInfo{Name: ServerName, Version: s.Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return toolsListResult{Tools: s.toolDefs()}, nil
	case "tools/call":
		return s.callTool(ctx, req.Params)
	default:
		return nil, &rpcError{codeMethodNotFound, fmt.Sprintf("unknown method %q", req.Method)}
	}
}

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

// capabilities advertises tools and nothing else. Resources and prompts are
// out of scope until we know how agents actually use the tools; advertising
// them empty would be a promise this server does not keep.
type capabilities struct {
	Tools map[string]any `json:"tools"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func marshalReply(r response) ([]byte, error) {
	out, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("marshalling mcp reply: %w", err)
	}
	return out, nil
}
```

A JSON array body (a JSON-RPC batch, removed from MCP in this revision) fails `json.Unmarshal` into `request` and comes back as `codeParse`... which is not what the test asserts. Fix it explicitly rather than letting the test bend: add this to `Handle`, immediately after the `var req request` declaration and **before** the `json.Unmarshal`:

```go
	if first := firstNonSpace(body); first == '[' {
		return marshalReply(response{JSONRPC: "2.0", Error: &rpcError{codeInvalidRequest,
			"JSON-RPC batching is not supported; send one request per call"}})
	}
```

and this helper at the bottom of the file:

```go
// firstNonSpace is how a batch is told from a single request without decoding
// twice. A batch is refused by name rather than falling out as a parse error,
// so a client that still sends one is told what to do instead of what broke.
func firstNonSpace(b []byte) byte {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return c
		}
	}
	return 0
}
```

- [ ] **Step 4: Add the seams the next tasks fill**

`dispatch` calls `s.toolDefs()` and `s.callTool`, which do not exist yet. Create `hub/internal/mcp/tools.go` with just enough for this task to compile and the tests to pass — Task 5 fills it in:

```go
package mcp

import (
	"context"
	"encoding/json"
)

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

// toolDef is a tool as it appears in tools/list.
type toolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type property struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

// toolError is a failure the MODEL should see and act on — an unknown service,
// a window it cannot parse — as opposed to a protocol or store failure. It
// comes back as a tools/call RESULT with isError set, not as a JSON-RPC error,
// which is what lets an agent correct itself instead of being handed a
// transport failure it cannot interpret.
type toolError struct {
	Message    string   `json:"error"`
	DidYouMean []string `json:"didYouMean,omitempty"`
}

func (e *toolError) Error() string { return e.Message }

func (s *Server) toolDefs() []toolDef { return nil }

func (s *Server) callTool(_ context.Context, _ json.RawMessage) (any, *rpcError) {
	return nil, &rpcError{codeMethodNotFound, "no tools yet"}
}
```

`toolError` is declared here rather than in `args.go` because both the argument decoding and every tool raise it, and it is the type the whole tool-error contract turns on.

Also create `hub/internal/mcp/store.go`, the narrow slice of `storage.Store` this package reads — narrow on purpose, exactly as `rates.Store` is: the API wires it from the live store, the tests hand it `storagetest.Fake`, and this package stays free of the rest of storage:

```go
package mcp

import (
	"context"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Store is the slice of storage.Store the tools read. Every method here is one
// the REST handlers already call, which is the point: the two surfaces answer
// from the same reads, so they cannot drift into reporting different numbers
// for the same window.
type Store interface {
	ListServices(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceStats, error)
	ServiceEdges(ctx context.Context, q storage.ServiceQuery) ([]storage.ServiceEdge, error)
	SearchTraces(ctx context.Context, q storage.TraceQuery) (storage.TracePage, error)
	GetTrace(ctx context.Context, tenants []string, traceID string) (storage.Trace, error)
	SearchLogs(ctx context.Context, q storage.LogQuery) (storage.LogPage, error)
	SearchErrorIssues(ctx context.Context, q storage.ErrorIssueQuery) ([]storage.ErrorIssue, error)
	LoadAlertStates(ctx context.Context, tenant string) ([]storage.AlertState, error)
}
```

These two stubs exist so this task's tests can run against a real dispatch. Task 5 replaces both bodies; nothing else in `tools.go` changes.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd hub && go test ./internal/mcp/... -v
```

Expected: PASS — `TestInitialize`, `TestNotificationGetsNoReply`, `TestPing`, and all five `TestProtocolErrors` sub-tests.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/mcp/protocol.go hub/internal/mcp/protocol_test.go hub/internal/mcp/tools.go hub/internal/mcp/store.go
git commit -m "feat(hub): the MCP protocol envelope

initialize, ping and the dispatch, with the two rules a client desyncs
on if we get them wrong: a notification takes no reply, and a batch is
refused by name rather than falling out as a parse error."
```

---

### Task 4: Mount `POST /mcp` behind the module and the existing auth

**Files:**
- Create: `hub/internal/api/mcp.go`
- Create: `hub/internal/api/mcp_test.go`
- Modify: `hub/internal/api/router.go` (after the `modules.AI` block, at the end of `Register`)

- [ ] **Step 1: Write the failing test**

Create `hub/internal/api/mcp_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

const mcpInitBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`

func mcpPost(mux *http.ServeMux, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// The module gate: an install that did not opt in has no route at all, rather
// than a route that refuses. 404 is the honest answer — there is nothing here.
func TestMCPAbsentWithoutTheModule(t *testing.T) {
	set, err := modules.Parse("core,logs")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return &storagetest.Fake{} }, Config{Modules: set})
	if w := mcpPost(mux, mcpInitBody, nil); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", w.Code, w.Body)
	}
}

func TestMCPRefusesAnUnauthenticatedCall(t *testing.T) {
	mux, _, _, _ := bearerMux(t, Config{})
	if w := mcpPost(mux, mcpInitBody, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (%s)", w.Code, w.Body)
	}
}

// A personal API token is a first-class credential here: it authenticates as
// its owner and reads that owner's projects. That is the entire authorization
// story for this client — there is no second one.
func TestMCPAcceptsABearerTokenAndKeepsItsScope(t *testing.T) {
	mux, _, _, raw := bearerMux(t, Config{})
	hdr := bearer(raw)
	hdr["X-Avuru-Tenant"] = "prod"

	w := mcpPost(mux, mcpInitBody, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body)
	}
	var got struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Result.ProtocolVersion == "" || got.Result.ServerInfo.Name == "" {
		t.Errorf("initialize returned an empty handshake: %+v", got.Result)
	}

	// The same token outside its owner's grants is refused here exactly as it
	// is on /api/v1/services: the tenancy rule is one rule, not one per client.
	hdr["X-Avuru-Tenant"] = "payments"
	if w := mcpPost(mux, mcpInitBody, hdr); w.Code != http.StatusForbidden {
		t.Fatalf("token outside its owner's grants: %d, want 403 (%s)", w.Code, w.Body)
	}
}

// A notification is delivered, not answered — 202 with no body, which is how a
// client tells "you got it" from "here is your reply".
func TestMCPNotificationIs202(t *testing.T) {
	mux, _, _, raw := bearerMux(t, Config{})
	hdr := bearer(raw)
	hdr["X-Avuru-Tenant"] = "prod"
	w := mcpPost(mux, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, hdr)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", w.Code, w.Body)
	}
	if w.Body.Len() != 0 {
		t.Errorf("notification answered with a body: %s", w.Body)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd hub && go test ./internal/api/... -run TestMCP -v
```

Expected: FAIL — every case except `TestMCPAbsentWithoutTheModule` returns 405 (the mux has no `POST /mcp`, and `MethodNotAllowed` is what Go's mux answers for an unmatched method on an existing path prefix). `TestMCPAbsentWithoutTheModule` passes vacuously and will stay passing for the right reason after Step 3.

- [ ] **Step 3: Write the handler**

Create `hub/internal/api/mcp.go`:

```go
package api

import (
	"io"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/mcp"
)

// maxMCPBody bounds a JSON-RPC request. Tool arguments are filters — a service
// name, a window, a row limit — so the 64 KiB every other JSON endpoint here
// uses is orders of magnitude more than an honest call needs.
const maxMCPBody = 1 << 16

// handleMCP serves one Model Context Protocol request.
//
// Everything about WHO is asking has already happened by the time this runs:
// secured() resolved the credential (a personal API token, or a session),
// refused anyone without Viewer, and projectTenants below authorizes the
// project and expands it to the tenants the read may span. The mcp package
// makes no authorization decision of its own — which is exactly what keeps
// this client on the one permission model instead of growing a parallel copy
// that drifts (design/2026-08-13-api-tokens.md).
func (a *API) handleMCP(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
	if err != nil {
		return err // *http.MaxBytesError → 413, handled centrally
	}
	srv := &mcp.Server{
		Store:   store,
		Modules: a.modules,
		Tenant:  tenant,
		Tenants: tenants,
		Version: Version,
		Actor:   actorName(r),
	}
	reply, err := srv.Handle(r.Context(), body)
	if err != nil {
		return err
	}
	if reply == nil {
		// A notification: delivered, not answered.
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reply)
	return nil
}

// actorName is who the audit line names. Empty when auth is off, which the
// audit line reports as such rather than inventing a user.
func actorName(r *http.Request) string {
	id := identityFrom(r.Context())
	if id == nil {
		return ""
	}
	if id.Email != "" {
		return id.Email
	}
	return id.UserID
}
```

- [ ] **Step 4: Mount the route**

In `hub/internal/api/router.go`, add at the end of `Register`, immediately after the `if active.Enabled(modules.AI) { … }` block:

```go
	if active.Enabled(modules.MCP) {
		// Not under /api/v1: MCP clients are configured with a bare server URL
		// and this is a protocol endpoint, not a REST resource — versioning it
		// alongside the resource API would imply the two move together.
		//
		// secured() and nothing else: a personal API token resolves its
		// owner's LIVE permissions, so the tools read exactly what that person
		// reads in the UI. It also brings the CSRF origin check along, which
		// costs a header-setting client nothing (it sends no Origin) and is
		// what a browser-hosted client should be held to.
		mux.Handle("POST /mcp", a.secured(auth.RoleViewer, a.handleMCP))
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd hub && go test ./internal/api/... -run TestMCP -v
```

Expected: PASS — all four cases.

- [ ] **Step 6: Prove nothing else moved**

```bash
cd hub && go test ./... && golangci-lint run
```

Expected: all packages PASS, lint clean. In particular `TestRoutes` and `TestPermissionsMatrix` are unchanged: the new route registers through the same `routeIndex`, so the permissions matrix reports it without being told.

- [ ] **Step 7: Commit**

```bash
git add hub/internal/api/mcp.go hub/internal/api/mcp_test.go hub/internal/api/router.go
git commit -m "feat(hub): serve POST /mcp behind the module and the existing auth

No new authorization: secured() resolves the credential and projectTenants
expands the project, exactly as on every other read route, and the mcp
package is handed the answer. A token outside its owner's grants is
refused here for the same reason it is on /api/v1/services."
```

---

### Task 5: Windows, row limits and resolving a service name

**Files:**
- Create: `hub/internal/mcp/args.go`
- Create: `hub/internal/mcp/args_test.go`

- [ ] **Step 1: Write the failing test**

Create `hub/internal/mcp/args_test.go`:

```go
package mcp

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

var testNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func TestTimeRange(t *testing.T) {
	tests := []struct {
		name        string
		args        windowArgs
		wantStart   time.Time
		wantEnd     time.Time
		wantToolErr bool
	}{
		{"absent means the last hour", windowArgs{}, testNow.Add(-time.Hour), testNow, false},
		{"relative window", windowArgs{Window: "15m"}, testNow.Add(-15 * time.Minute), testNow, false},
		{"a day", windowArgs{Window: "24h"}, testNow.Add(-24 * time.Hour), testNow, false},
		{"absolute pair", windowArgs{
			Start: "2026-09-01T09:00:00Z", End: "2026-09-01T10:00:00Z"},
			time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), false},
		{"end alone anchors the window", windowArgs{Window: "30m", End: "2026-09-01T10:00:00Z"},
			time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC), time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), false},
		{"unparseable window", windowArgs{Window: "last tuesday"}, time.Time{}, time.Time{}, true},
		{"negative window", windowArgs{Window: "-1h"}, time.Time{}, time.Time{}, true},
		{"unparseable start", windowArgs{Start: "yesterday"}, time.Time{}, time.Time{}, true},
		{"end before start", windowArgs{
			Start: "2026-09-01T10:00:00Z", End: "2026-09-01T09:00:00Z"}, time.Time{}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.args.timeRange(testNow)
			var terr *toolError
			if tt.wantToolErr {
				if !errors.As(err, &terr) {
					t.Fatalf("err = %v, want a toolError the model can read", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Start.Equal(tt.wantStart) || !got.End.Equal(tt.wantEnd) {
				t.Errorf("range = %s..%s, want %s..%s", got.Start, got.End, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestClampRows(t *testing.T) {
	tests := []struct{ in, want int }{
		{0, defaultRows}, {-3, defaultRows}, {5, 5}, {maxRows, maxRows}, {maxRows + 1, maxRows}, {100000, maxRows},
	}
	for _, tt := range tests {
		if got := clampRows(tt.in, defaultRows, maxRows); got != tt.want {
			t.Errorf("clampRows(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func fakeWithServices(names ...string) *storagetest.Fake {
	f := &storagetest.Fake{}
	for _, n := range names {
		f.Services = append(f.Services, storage.ServiceStats{Name: n, SpanCount: 10})
	}
	return f
}

func serverWith(f *storagetest.Fake) *Server {
	return &Server{Store: f, Modules: modules.AllSet(), Tenant: "default",
		Tenants: []string{"default"}, Version: "test", Now: func() time.Time { return testNow }}
}

func TestResolveService(t *testing.T) {
	s := serverWith(fakeWithServices("payment-api", "payments-worker", "frontend"))
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	if got, err := s.resolveService(context.Background(), tr, "payment-api"); err != nil || got != "payment-api" {
		t.Fatalf("exact match: got %q, %v", got, err)
	}
	// The stored spelling wins, so everything downstream filters on a name the
	// store will actually match.
	if got, err := s.resolveService(context.Background(), tr, "Payment-API"); err != nil || got != "payment-api" {
		t.Fatalf("case-insensitive match: got %q, %v", got, err)
	}
}

// The rule this feature turns on: an unknown name is an ERROR naming the near
// matches, never an empty list. A model handed [] concludes the service is
// dead and reports that with confidence.
func TestResolveServiceUnknownNamesNearMatches(t *testing.T) {
	s := serverWith(fakeWithServices("payment-api", "payments-worker", "frontend"))
	tr := storage.TimeRange{Start: testNow.Add(-time.Hour), End: testNow}

	_, err := s.resolveService(context.Background(), tr, "paiment-api")
	var terr *toolError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v, want a toolError", err)
	}
	if len(terr.DidYouMean) == 0 || terr.DidYouMean[0] != "payment-api" {
		t.Errorf("didYouMean = %v, want payment-api first", terr.DidYouMean)
	}
	if terr.Message == "" {
		t.Error("the error must say what was not found, and over which window")
	}
}

func TestNearest(t *testing.T) {
	known := []string{"payment-api", "payments-worker", "frontend", "cart"}
	tests := []struct {
		name string
		want []string
		in   string
	}{
		{"a typo finds the neighbour", []string{"payment-api"}, "paymnet-api"},
		{"a prefix finds both", []string{"payment-api", "payments-worker"}, "payment"},
		{"nothing close suggests nothing", nil, "zzzzzzzzzzzzzz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nearest(tt.in, known, 5); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("nearest(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd hub && go test ./internal/mcp/... -run 'TestTimeRange|TestClampRows|TestResolveService|TestNearest'
```

Expected: FAIL to build — `undefined: windowArgs`, `undefined: clampRows`, `undefined: nearest`, `s.resolveService undefined`.

- [ ] **Step 3: Write the argument layer**

Create `hub/internal/mcp/args.go`:

```go
package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// defaultWindow is what a tool reads when the caller names no range: an hour,
// the span of an incident someone is currently inside.
const defaultWindow = time.Hour

// Row bounds. defaultRows is what a model can read without losing the thread;
// maxRows is the most it may ask for. Every payload that hits either says so —
// a top-20 with no marker gets reasoned about as the whole estate, which is
// the same way of being confidently wrong that v0.11's "reported no usage"
// bucket exists to prevent.
const (
	defaultRows = 20
	maxRows     = 100
	maxSpans    = 500
)

// windowArgs is the range vocabulary every tool accepts.
//
// The REST API is absolute-only — parseTimeRange reads RFC3339 start/end and
// nothing else. An agent reasons in "the last twenty minutes", and making it
// compute two timestamps to ask that is friction with no upside. So the
// relative form is translated here, at the tool boundary, and the absolute
// pair stays available for the caller that has one.
type windowArgs struct {
	Window string `json:"window,omitempty"`
	Start  string `json:"start,omitempty"`
	End    string `json:"end,omitempty"`
}

// timeRange resolves the arguments against now. End defaults to now and Start
// to End minus the window, so naming either one alone still yields a range.
func (w windowArgs) timeRange(now time.Time) (storage.TimeRange, error) {
	d := defaultWindow
	if w.Window != "" {
		parsed, err := time.ParseDuration(w.Window)
		if err != nil || parsed <= 0 {
			return storage.TimeRange{}, &toolError{Message: fmt.Sprintf(
				"window %q is not a positive duration; use a form like 15m, 1h or 24h", w.Window)}
		}
		d = parsed
	}
	end := now
	if w.End != "" {
		t, err := time.Parse(time.RFC3339, w.End)
		if err != nil {
			return storage.TimeRange{}, &toolError{Message: fmt.Sprintf("end %q is not an RFC3339 timestamp", w.End)}
		}
		end = t
	}
	start := end.Add(-d)
	if w.Start != "" {
		t, err := time.Parse(time.RFC3339, w.Start)
		if err != nil {
			return storage.TimeRange{}, &toolError{Message: fmt.Sprintf("start %q is not an RFC3339 timestamp", w.Start)}
		}
		start = t
	}
	if !end.After(start) {
		return storage.TimeRange{}, &toolError{Message: "end must be after start"}
	}
	return storage.TimeRange{Start: start.UTC(), End: end.UTC()}, nil
}

// windowDTO is how a payload reports the range it actually read, so a model
// never has to assume which window its numbers came from.
type windowDTO struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func toWindowDTO(tr storage.TimeRange) windowDTO {
	return windowDTO{Start: tr.Start.Format(time.RFC3339), End: tr.End.Format(time.RFC3339)}
}

// clampRows bounds a requested row count: absent (0 or less) takes def,
// anything over max is reduced to it. Silently — what matters is that the
// payload reports what it returned and whether more existed.
func clampRows(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	default:
		return v
	}
}

// serviceQuery is the ServiceQuery every tool builds: the tenant set the API
// resolved, the window, and the same auxiliary-traffic exclusion the screens
// default to — health checks and scrapes are not what anyone is investigating.
func (s *Server) serviceQuery(tr storage.TimeRange) storage.ServiceQuery {
	return storage.ServiceQuery{Tenant: s.Tenant, Tenants: s.Tenants, Range: tr, ExcludeAux: true}
}

// resolveService turns the name an agent asked for into one this estate knows,
// or a toolError naming the closest matches.
//
// Returning no rows for an unknown name is the cheaper answer and the
// dangerous one: a model handed an empty list for a misspelling reads it as
// "this service is dead" and says so with confidence. Naming the near matches
// turns a dead end into the next question.
func (s *Server) resolveService(ctx context.Context, tr storage.TimeRange, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", &toolError{Message: "service is required"}
	}
	services, err := s.Store.ListServices(ctx, s.serviceQuery(tr))
	if err != nil {
		return "", fmt.Errorf("listing services: %w", err)
	}
	known := make([]string, 0, len(services))
	for _, svc := range services {
		if svc.Name == name {
			return svc.Name, nil
		}
		known = append(known, svc.Name)
	}
	for _, k := range known {
		if strings.EqualFold(k, name) {
			return k, nil // the stored spelling wins — it is what filters match
		}
	}
	return "", &toolError{
		Message: fmt.Sprintf("no service named %q reported anything between %s and %s",
			name, tr.Start.Format(time.RFC3339), tr.End.Format(time.RFC3339)),
		DidYouMean: nearest(name, known, 5),
	}
}

// nearest ranks known names by closeness to want: containment either way first
// (a model that asked for "payment" should be shown "payment-api"), then edit
// distance, budgeted by the name's own length so an unrelated service is never
// suggested.
func nearest(want string, known []string, n int) []string {
	type scored struct {
		name string
		dist int
	}
	lw := strings.ToLower(want)
	budget := len(lw)/3 + 1
	var ranked []scored
	for _, k := range known {
		lk := strings.ToLower(k)
		d := levenshtein(lw, lk)
		switch {
		case strings.Contains(lk, lw) || strings.Contains(lw, lk):
			d = 0
		case d > budget:
			continue
		}
		ranked = append(ranked, scored{k, d})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].dist != ranked[j].dist {
			return ranked[i].dist < ranked[j].dist
		}
		return ranked[i].name < ranked[j].name
	})
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	var names []string
	for _, r := range ranked {
		names = append(names, r.name)
	}
	return names
}

// levenshtein is the classic two-row edit distance — local rather than a
// dependency, because the whole use is ranking a handful of service names.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// ms, ratio and perSec are the three conversions every payload does. Durations
// reach an agent as float milliseconds (the unit the REST API already speaks)
// and counts reach it as a rate or a fraction, never as a raw pair it has to
// divide — a model that divides is a model that can divide wrong.
func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func ratio(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func perSec(count uint64, tr storage.TimeRange) float64 {
	secs := tr.End.Sub(tr.Start).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(count) / secs
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd hub && go test ./internal/mcp/... -v
```

Expected: PASS — `TestTimeRange` (9 sub-tests), `TestClampRows`, `TestResolveService`, `TestResolveServiceUnknownNamesNearMatches`, `TestNearest` (3 sub-tests), plus Task 3's protocol tests.

- [ ] **Step 5: Commit**

```bash
git add hub/internal/mcp/args.go hub/internal/mcp/args_test.go
git commit -m "feat(hub): mcp windows, row bounds and service resolution

Three decisions the tools all inherit: a relative window is translated
here because an agent reasons in \"the last twenty minutes\"; every row
count is bounded; and an unknown service name is an error naming the near
matches, never an empty list a model will read as a dead service."
```

---

### Task 6: The tool registry, the audit line, and the first tool

Replaces the two stubs from Task 3 with the real registry, and lands one tool through it so `tools/list`, `tools/call` and the audit line are all exercised by something real.

**Files:**
- Modify: `hub/internal/mcp/tools.go` (replace `toolDefs` and `callTool`; keep the types)
- Create: `hub/internal/mcp/audit.go`
- Create: `hub/internal/mcp/audit_test.go`
- Create: `hub/internal/mcp/signals.go`
- Create: `hub/internal/mcp/signals_test.go`

- [ ] **Step 1: Write the failing tests**

Create `hub/internal/mcp/signals_test.go`:

```go
package mcp

import (
	"encoding/json"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// callTool runs one tool through the full JSON-RPC path and returns the
// decoded payload plus whether the result was flagged as a tool error. Going
// through Handle rather than calling run() directly is the point: it is the
// path a client takes, audit line included.
func callTool(t *testing.T, s *Server, name, args string) (map[string]any, bool) {
	t.Helper()
	if args == "" {
		args = "{}"
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	got := call(t, s, body)
	if got["error"] != nil {
		t.Fatalf("%s: protocol error %v", name, got["error"])
	}
	res, ok := got["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no result in %v", name, got)
	}
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("%s: no content in %v", name, res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("%s: payload is not JSON: %s", name, text)
	}
	isErr, _ := res["isError"].(bool)
	return payload, isErr
}

func TestListServices(t *testing.T) {
	f := fakeWithServices("frontend", "payment-api")
	f.Services[0] = storage.ServiceStats{Name: "frontend", SpanCount: 3600, ErrorCount: 0}
	f.Services[1] = storage.ServiceStats{Name: "payment-api", SpanCount: 1800, ErrorCount: 180}
	s := serverWith(f)

	payload, isErr := callTool(t, s, "list_services", `{"window":"1h"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	services, _ := payload["services"].([]any)
	if len(services) != 2 {
		t.Fatalf("got %d services, want 2: %v", len(services), payload)
	}
	// Worst first: a truncated list has to be the part that matters.
	first, _ := services[0].(map[string]any)
	if first["service"] != "payment-api" {
		t.Errorf("first row = %v, want payment-api (the one with errors)", first["service"])
	}
	if first["errorRate"] != 0.1 {
		t.Errorf("errorRate = %v, want 0.1", first["errorRate"])
	}
	if first["ratePerSec"] != 0.5 {
		t.Errorf("ratePerSec = %v, want 0.5 (1800 spans over an hour)", first["ratePerSec"])
	}
	if payload["truncated"] != false {
		t.Errorf("truncated = %v, want false", payload["truncated"])
	}
}

func TestListServicesUnhealthyOnly(t *testing.T) {
	f := fakeWithServices("frontend", "payment-api")
	f.Services[0] = storage.ServiceStats{Name: "frontend", SpanCount: 100}
	f.Services[1] = storage.ServiceStats{Name: "payment-api", SpanCount: 100, ErrorCount: 5}
	payload, _ := callTool(t, serverWith(f), "list_services", `{"unhealthy_only":true}`)
	services, _ := payload["services"].([]any)
	if len(services) != 1 {
		t.Fatalf("got %d services, want only the failing one: %v", len(services), payload)
	}
}

// A bounded response has to SAY it is bounded. A model handed a top-N with no
// marker reasons about it as the whole estate.
func TestListServicesTruncationIsAnnounced(t *testing.T) {
	f := &storagetest.Fake{}
	for i := 0; i < 30; i++ {
		f.Services = append(f.Services, storage.ServiceStats{Name: fmt.Sprintf("svc-%02d", i), SpanCount: uint64(i + 1)})
	}
	payload, _ := callTool(t, serverWith(f), "list_services", `{"limit":10}`)
	services, _ := payload["services"].([]any)
	if len(services) != 10 {
		t.Fatalf("got %d services, want 10", len(services))
	}
	if payload["truncated"] != true {
		t.Errorf("truncated = %v, want true", payload["truncated"])
	}
	if payload["total"] != float64(30) {
		t.Errorf("total = %v, want 30 — the count is how a model knows what it is missing", payload["total"])
	}
}

// An argument the model invented must be refused, not ignored. Silence gets
// read as "the filter was applied", and every number after that is wrong.
func TestUnknownArgumentIsRefused(t *testing.T) {
	payload, isErr := callTool(t, serverWith(fakeWithServices("a")), "list_services", `{"servicee":"a"}`)
	if !isErr {
		t.Fatalf("an unknown argument was accepted: %v", payload)
	}
	if payload["error"] == nil {
		t.Errorf("tool error carried no message: %v", payload)
	}
}

func TestToolsListNamesTheTools(t *testing.T) {
	got := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	res, _ := got["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("tools/list is empty: %v", res)
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] == nil || first["description"] == nil || first["inputSchema"] == nil {
		t.Errorf("a tool definition is missing name/description/inputSchema: %v", first)
	}
}

func TestUnknownToolIsInvalidParams(t *testing.T) {
	got := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"drop_database"}}`)
	e, ok := got["error"].(map[string]any)
	if !ok || e["code"] != float64(codeInvalidParams) {
		t.Errorf("unknown tool = %v, want an invalid-params error", got)
	}
}
```

Add the two imports this file needs at the top (`fmt` and the storagetest package):

```go
import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)
```

Create `hub/internal/mcp/audit_test.go`:

```go
package mcp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The audit obligation, asserted: the line says WHO read WHAT SHAPE of data,
// and carries none of the data. Logging the rows would put the log bodies this
// module exports into a second place they were never meant to be.
func TestAuditLineNamesTheReadAndNotTheRows(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	f := fakeWithServices("payment-api")
	f.Services[0] = storage.ServiceStats{Name: "payment-api", SpanCount: 10}
	s := serverWith(f)
	s.Actor = "bot@x.io"

	if _, isErr := callTool(t, s, "list_services", `{"window":"1h"}`); isErr {
		t.Fatal("call failed")
	}

	line := buf.String()
	for _, want := range []string{`"tool":"list_services"`, `"actor":"bot@x.io"`, `"rows":1`, `"project":"default"`} {
		if !strings.Contains(line, want) {
			t.Errorf("audit line is missing %s: %s", want, line)
		}
	}
	if !strings.Contains(line, "1h") {
		t.Errorf("audit line does not record the arguments: %s", line)
	}
	if strings.Contains(line, "payment-api") {
		t.Errorf("audit line carried a row it returned: %s", line)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd hub && go test ./internal/mcp/... -run 'TestListServices|TestToolsList|TestUnknown|TestAudit'
```

Expected: FAIL to build — `undefined: runListServices`, `undefined: listServicesDef`, and `tools/list` returning `null` where the test wants entries.

- [ ] **Step 3: Write the registry and the dispatch**

Replace the last two functions in `hub/internal/mcp/tools.go` (`toolDefs` and `callTool`) with the following, and extend the file's imports to `bytes`, `context`, `encoding/json`, `errors`, `fmt` and `github.com/avuru/avuru-obs/hub/internal/modules`:

```go
// tool is one MCP tool: its wire definition plus the function that runs it.
type tool struct {
	def toolDef
	// module must be active for this tool to exist. Empty means core.
	module modules.Name
	run    func(ctx context.Context, s *Server, args json.RawMessage) (any, error)
}

// tools is the registry, in the order tools/list reports them. A slice and not
// a map, so the list is stable across calls and a diff of it reads.
func (s *Server) tools() []tool {
	return []tool{
		{def: listServicesDef, run: runListServices},
	}
}

// toolDefs is tools/list: the tools this install actually has.
//
// A tool whose module is off is ABSENT, not present-and-always-failing. That
// is the capabilities pattern the sidebar already uses to hide a screen rather
// than render a gap, and it matters more here: a model handed a tool that
// exists will call it, read the failure as a fact about the estate, and reason
// onward from it.
func (s *Server) toolDefs() []toolDef {
	out := make([]toolDef, 0, len(s.tools()))
	for _, t := range s.tools() {
		if t.module != "" && !s.Modules.Enabled(t.module) {
			continue
		}
		out = append(out, t.def)
	}
	return out
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// callResult is a tools/call reply. The payload is returned as JSON inside a
// text content block rather than as structuredContent: every MCP client can
// read text, structured output is negotiated per tool through an output
// schema, and one shape that always works beats two that sometimes do.
type callResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type textContent struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var p callParams
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return nil, &rpcError{codeInvalidParams, `"params" must be an object carrying a tool "name"`}
	}
	if p.Name == "" {
		return nil, &rpcError{codeInvalidParams, `"name" is required`}
	}
	for _, t := range s.tools() {
		if t.def.Name != p.Name {
			continue
		}
		if t.module != "" && !s.Modules.Enabled(t.module) {
			// Not method-not-found: the tool exists, this install does not run
			// it. Saying which module is missing lets an agent report that,
			// instead of concluding the estate has no logs.
			return nil, &rpcError{codeInvalidParams, fmt.Sprintf(
				"tool %q needs the %q module, which is not enabled on this install", p.Name, t.module)}
		}
		return s.runTool(ctx, t, p.Arguments)
	}
	return nil, &rpcError{codeInvalidParams, fmt.Sprintf("unknown tool %q", p.Name)}
}

// runTool runs one tool, turns its payload into the wire result, and writes
// the audit line whichever way it went.
func (s *Server) runTool(ctx context.Context, t tool, args json.RawMessage) (any, *rpcError) {
	started := s.now()
	payload, err := t.run(ctx, s, args)
	took := s.now().Sub(started)

	var terr *toolError
	switch {
	case errors.As(err, &terr):
		// A failure the model should see and correct: it comes back as a
		// RESULT with isError, which is the only form an agent can reason about.
		s.logCall(t.def.Name, args, 0, took, err)
		return jsonResult(terr, true)
	case err != nil:
		s.logCall(t.def.Name, args, 0, took, err)
		// Deliberately shapeless: a store error's text can carry a query, and
		// a query can carry a name the caller holds no grant for. The detail
		// goes to the log, not to the caller.
		return nil, &rpcError{codeInternal, "the telemetry store could not answer this call"}
	}
	s.logCall(t.def.Name, args, rowsOf(payload), took, nil)
	return jsonResult(payload, false)
}

func jsonResult(payload any, isErr bool) (any, *rpcError) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &rpcError{codeInternal, "could not encode the tool result"}
	}
	return callResult{Content: []textContent{{Type: "text", Text: string(body)}}, IsError: isErr}, nil
}

// decodeArgs decodes a tool's arguments and REFUSES an unknown field. A model
// that invents an argument has to be told: ignoring it reads back as "the
// filter was applied", and every number after that is wrong.
func decodeArgs(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return &toolError{Message: fmt.Sprintf("could not read the arguments: %v", err)}
	}
	return nil
}
```

- [ ] **Step 4: Write the audit line**

Create `hub/internal/mcp/audit.go`:

```go
package mcp

import (
	"encoding/json"
	"log/slog"
	"time"
)

// rowCounter is implemented by every tool payload, so the audit line can say
// how MUCH came back without touching WHAT did.
type rowCounter interface{ rows() int }

func rowsOf(payload any) int {
	if rc, ok := payload.(rowCounter); ok {
		return rc.rows()
	}
	return 0
}

// logCall records one tool call: who asked, which tool, with which arguments,
// how many rows came back, how long it took — and never the rows themselves.
//
// This discharges the obligation the module's opt-in is built on
// (design/2026-09-01-mcp-server.md): an operator who turns this on must be
// able to answer "what did the agent read, and whose token did it use" from
// the hub's own logs. That question is answered by the SHAPE of the read.
// Logging the content would copy the log bodies this module exports into a
// second place they were never meant to be, which is the opposite of the point.
func (s *Server) logCall(name string, args json.RawMessage, rows int, took time.Duration, err error) {
	actor := s.Actor
	if actor == "" {
		// Auth off, or an anonymous (demo) identity. Named rather than left
		// blank: "we do not know who" is itself worth reading in an audit log.
		actor = "anonymous"
	}
	attrs := []any{
		"tool", name,
		"actor", actor,
		"project", s.Tenant,
		"args", string(args),
		"rows", rows,
		"duration_ms", took.Milliseconds(),
	}
	if err != nil {
		slog.Warn("mcp tool call failed", append(attrs, "error", err.Error())...)
		return
	}
	slog.Info("mcp tool call", attrs...)
}
```

- [ ] **Step 5: Write the first tool**

Create `hub/internal/mcp/signals.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The window properties every tool's schema repeats. Declared once so the
// three of them cannot drift apart across six tools.
func windowProperties() map[string]property {
	return map[string]property{
		"window": {Type: "string", Description: `Relative window ending now, e.g. "15m", "1h", "24h". Default "1h".`},
		"start":  {Type: "string", Description: "Absolute range start (RFC3339). Overrides window."},
		"end":    {Type: "string", Description: "Absolute range end (RFC3339). Defaults to now."},
	}
}

// withWindow returns the window properties plus the tool's own.
func withWindow(own map[string]property) map[string]property {
	out := windowProperties()
	for k, v := range own {
		out[k] = v
	}
	return out
}

var listServicesDef = toolDef{
	Name: "list_services",
	Description: "List the services reporting telemetry in a window, with request rate, error rate and latency percentiles (p50/p95/p99). " +
		"Use it to find a service when you only have a symptom, or to see what is unhealthy right now. Worst first.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"unhealthy_only": {Type: "boolean", Description: "Return only services with a non-zero error rate."},
			"limit":          {Type: "integer", Description: "Maximum rows (default 20, maximum 100)."},
		}),
	},
}

type listServicesArgs struct {
	windowArgs
	UnhealthyOnly bool `json:"unhealthy_only,omitempty"`
	Limit         int  `json:"limit,omitempty"`
}

type serviceRow struct {
	Service    string  `json:"service"`
	RatePerSec float64 `json:"ratePerSec"`
	ErrorRate  float64 `json:"errorRate"`
	P50Ms      float64 `json:"p50Ms"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
	SpanCount  uint64  `json:"spanCount"`
}

func toServiceRow(s storage.ServiceStats, tr storage.TimeRange) serviceRow {
	return serviceRow{
		Service:    s.Name,
		RatePerSec: perSec(s.SpanCount, tr),
		ErrorRate:  ratio(s.ErrorCount, s.SpanCount),
		P50Ms:      ms(s.P50),
		P95Ms:      ms(s.P95),
		P99Ms:      ms(s.P99),
		SpanCount:  s.SpanCount,
	}
}

type listServicesPayload struct {
	Window    windowDTO    `json:"window"`
	Services  []serviceRow `json:"services"`
	Returned  int          `json:"returned"`
	Total     int          `json:"total"`
	Truncated bool         `json:"truncated"`
}

func (p listServicesPayload) rows() int { return p.Returned }

func runListServices(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a listServicesArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	services, err := s.Store.ListServices(ctx, s.serviceQuery(tr))
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	rows := make([]serviceRow, 0, len(services))
	for _, svc := range services {
		if a.UnhealthyOnly && svc.ErrorCount == 0 {
			continue
		}
		rows = append(rows, toServiceRow(svc, tr))
	}
	// Worst first, busiest next: whatever survives truncation has to be the
	// part worth reading.
	sort.Slice(rows, func(i, j int) bool {
		switch {
		case rows[i].ErrorRate != rows[j].ErrorRate:
			return rows[i].ErrorRate > rows[j].ErrorRate
		case rows[i].RatePerSec != rows[j].RatePerSec:
			return rows[i].RatePerSec > rows[j].RatePerSec
		default:
			return rows[i].Service < rows[j].Service
		}
	})
	total := len(rows)
	limit := clampRows(a.Limit, defaultRows, maxRows)
	truncated := total > limit
	if truncated {
		rows = rows[:limit]
	}
	return listServicesPayload{
		Window: toWindowDTO(tr), Services: rows,
		Returned: len(rows), Total: total, Truncated: truncated,
	}, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
cd hub && go test ./internal/mcp/... -v
```

Expected: PASS — everything from Tasks 3 and 5, plus `TestListServices`, `TestListServicesUnhealthyOnly`, `TestListServicesTruncationIsAnnounced`, `TestUnknownArgumentIsRefused`, `TestToolsListNamesTheTools`, `TestUnknownToolIsInvalidParams`, `TestAuditLineNamesTheReadAndNotTheRows`.

- [ ] **Step 7: Commit**

```bash
git add hub/internal/mcp/tools.go hub/internal/mcp/audit.go hub/internal/mcp/audit_test.go hub/internal/mcp/signals.go hub/internal/mcp/signals_test.go
git commit -m "feat(hub): the mcp tool registry, the audit line, and list_services

Three rules land with the first tool and hold for the other five: a
bounded response says it is bounded and reports the total, an invented
argument is refused rather than ignored, and every call is logged with
the actor, the tool, its arguments and the row count — never the rows."
```

---

### Task 7: `search_traces` and `get_trace`

**Files:**
- Modify: `hub/internal/mcp/signals.go` (append)
- Modify: `hub/internal/mcp/tools.go` (two registry entries)
- Modify: `hub/internal/mcp/signals_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `hub/internal/mcp/signals_test.go`:

```go
func TestSearchTraces(t *testing.T) {
	f := fakeWithServices("payment-api")
	f.Page = storage.TracePage{Traces: []storage.TraceSummary{{
		TraceID: "abc", RootService: "payment-api", RootOperation: "POST /pay",
		StartTime: testNow.Add(-time.Minute), Duration: 250 * time.Millisecond,
		SpanCount: 3, ErrorCount: 1, StatusCode: "Error",
	}}}
	s := serverWith(f)

	payload, isErr := callTool(t, s, "search_traces", `{"service":"payment-api","status":"error","min_duration_ms":100,"window":"30m"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	traces, _ := payload["traces"].([]any)
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1: %v", len(traces), payload)
	}
	first, _ := traces[0].(map[string]any)
	if first["traceId"] != "abc" || first["durationMs"] != float64(250) {
		t.Errorf("trace row = %v", first)
	}
	// The filters must reach storage, not be dropped on the floor between the
	// tool schema and the query — that is the whole failure mode of a second
	// read path.
	if f.LastTraceQuery.Service != "payment-api" {
		t.Errorf("service filter = %q, want payment-api", f.LastTraceQuery.Service)
	}
	if f.LastTraceQuery.Status != "error" {
		t.Errorf("status filter = %q, want error", f.LastTraceQuery.Status)
	}
	if f.LastTraceQuery.MinDuration != 100*time.Millisecond {
		t.Errorf("min duration = %v, want 100ms", f.LastTraceQuery.MinDuration)
	}
	if !f.LastTraceQuery.ExcludeAux {
		t.Error("auxiliary traffic must be excluded — health checks are not what anyone is investigating")
	}
}

// A misspelled service is an error naming the near matches, on every tool that
// takes one — not just on service_context.
func TestSearchTracesUnknownService(t *testing.T) {
	payload, isErr := callTool(t, serverWith(fakeWithServices("payment-api")), "search_traces", `{"service":"paymnt-api"}`)
	if !isErr {
		t.Fatalf("unknown service accepted: %v", payload)
	}
	hints, _ := payload["didYouMean"].([]any)
	if len(hints) == 0 || hints[0] != "payment-api" {
		t.Errorf("didYouMean = %v, want payment-api", hints)
	}
}

func TestSearchTracesRejectsAnInventedStatus(t *testing.T) {
	payload, isErr := callTool(t, serverWith(fakeWithServices("a")), "search_traces", `{"status":"weird"}`)
	if !isErr {
		t.Fatalf("invented status accepted: %v", payload)
	}
}

// The store reports "there is more" through its cursor; the payload has to
// pass that on, because a model reading 20 of 900 traces as "all of them" will
// conclude the failure is rarer than it is.
func TestSearchTracesTruncationFollowsTheCursor(t *testing.T) {
	f := fakeWithServices("a")
	f.Page = storage.TracePage{
		Traces:     []storage.TraceSummary{{TraceID: "one", StartTime: testNow}},
		NextCursor: &storage.TraceCursor{TraceID: "one", Timestamp: testNow},
	}
	payload, _ := callTool(t, serverWith(f), "search_traces", `{}`)
	if payload["truncated"] != true {
		t.Errorf("truncated = %v, want true", payload["truncated"])
	}
}

func TestGetTrace(t *testing.T) {
	// A 250ms root in payment-api with two children in ledger: the root's own
	// work is 250 - (100 + 40) = 110ms, and ledger accounts for 140ms.
	f := fakeWithServices("payment-api", "ledger")
	f.Traces = map[string]storage.Trace{"abc": {TraceID: "abc", Spans: []storage.Span{
		{SpanID: "root", Service: "payment-api", Operation: "POST /pay", Kind: "Server",
			StartTime: testNow, Duration: 250 * time.Millisecond, StatusCode: "Error"},
		{SpanID: "a", ParentSpanID: "root", Service: "ledger", Operation: "SELECT",
			StartTime: testNow, Duration: 100 * time.Millisecond},
		{SpanID: "b", ParentSpanID: "root", Service: "ledger", Operation: "INSERT",
			StartTime: testNow.Add(time.Millisecond), Duration: 40 * time.Millisecond},
	}}}

	payload, isErr := callTool(t, serverWith(f), "get_trace", `{"trace_id":"abc"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	spans, _ := payload["spans"].([]any)
	if len(spans) != 3 {
		t.Fatalf("got %d spans, want 3", len(spans))
	}
	services, _ := payload["services"].([]any)
	if len(services) != 2 {
		t.Fatalf("got %d services in the rollup, want 2: %v", len(services), payload["services"])
	}
	byName := map[string]map[string]any{}
	for _, s := range services {
		row, _ := s.(map[string]any)
		byName[row["service"].(string)] = row
	}
	if got := byName["ledger"]["selfTimeMs"]; got != float64(140) {
		t.Errorf("ledger self time = %v, want 140 (100 + 40)", got)
	}
	if got := byName["payment-api"]["selfTimeMs"]; got != float64(110) {
		t.Errorf("payment-api self time = %v, want 110 (250 minus what it waited on)", got)
	}
	// Biggest first: the answer to "where did the time go" is the first row.
	if first, _ := services[0].(map[string]any); first["service"] != "ledger" {
		t.Errorf("rollup starts with %v, want ledger", first["service"])
	}
	if byName["payment-api"]["errorCount"] != float64(1) {
		t.Errorf("payment-api errorCount = %v, want 1", byName["payment-api"]["errorCount"])
	}
}

// Concurrent children can outlast their parent's own clock. Self time floors
// at zero rather than going negative, which would corrupt the rollup.
func TestGetTraceSelfTimeNeverGoesNegative(t *testing.T) {
	f := fakeWithServices("a")
	f.Traces = map[string]storage.Trace{"x": {TraceID: "x", Spans: []storage.Span{
		{SpanID: "root", Service: "a", Duration: 10 * time.Millisecond, StartTime: testNow},
		{SpanID: "c1", ParentSpanID: "root", Service: "b", Duration: 30 * time.Millisecond, StartTime: testNow},
	}}}
	payload, _ := callTool(t, serverWith(f), "get_trace", `{"trace_id":"x"}`)
	for _, s := range payload["services"].([]any) {
		row, _ := s.(map[string]any)
		if row["selfTimeMs"].(float64) < 0 {
			t.Errorf("negative self time: %v", row)
		}
	}
}

func TestGetTraceNotFound(t *testing.T) {
	payload, isErr := callTool(t, serverWith(fakeWithServices("a")), "get_trace", `{"trace_id":"nope"}`)
	if !isErr {
		t.Fatalf("missing trace accepted: %v", payload)
	}
	if payload["error"] == nil {
		t.Errorf("no message: %v", payload)
	}
}
```

Add `"time"` to that file's imports.

`storagetest.Fake` already records `LastTraceQuery` (fake.go:161, assigned in `SearchTraces`), along with `LastLogQuery`, `LastIssueQuery` and `LastServiceQuery`. Nothing to add — assert against them directly.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd hub && go test ./internal/mcp/... -run 'TestSearchTraces|TestGetTrace'
```

Expected: FAIL — `unknown tool "search_traces"` / `unknown tool "get_trace"` from `callTool`'s protocol-error path.

- [ ] **Step 3: Write the two tools**

Append to `hub/internal/mcp/signals.go` (and extend its imports with `errors`, `strings` and `time`):

```go
var searchTracesDef = toolDef{
	Name: "search_traces",
	Description: "Find requests matching a filter: service, root operation, status, minimum duration. " +
		"Returns one row per trace with its root operation, duration, span count and error count. " +
		"Use it after service_context to see the actual failing or slow requests, then get_trace on one of them.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"service":         {Type: "string", Description: "Only traces touching this service."},
			"operation":       {Type: "string", Description: `Only traces whose root operation matches, e.g. "POST /checkout".`},
			"status":          {Type: "string", Description: "Only failed or only succeeded requests.", Enum: []string{"ok", "error"}},
			"min_duration_ms": {Type: "number", Description: "Only traces at least this slow, in milliseconds."},
			"order":           {Type: "string", Description: `Sort order; "newest" by default.`, Enum: []string{"newest", "oldest", "slowest"}},
			"limit":           {Type: "integer", Description: "Maximum rows (default 20, maximum 100)."},
		}),
	},
}

type searchTracesArgs struct {
	windowArgs
	Service       string  `json:"service,omitempty"`
	Operation     string  `json:"operation,omitempty"`
	Status        string  `json:"status,omitempty"`
	MinDurationMs float64 `json:"min_duration_ms,omitempty"`
	Order         string  `json:"order,omitempty"`
	Limit         int     `json:"limit,omitempty"`
}

type traceRow struct {
	TraceID       string  `json:"traceId"`
	RootService   string  `json:"rootService"`
	RootOperation string  `json:"rootOperation"`
	StartTime     string  `json:"startTime"`
	DurationMs    float64 `json:"durationMs"`
	SpanCount     uint64  `json:"spanCount"`
	ErrorCount    uint64  `json:"errorCount"`
	StatusCode    string  `json:"statusCode,omitempty"`
	HTTPStatus    uint16  `json:"httpStatus,omitempty"`
}

type searchTracesPayload struct {
	Window   windowDTO  `json:"window"`
	Traces   []traceRow `json:"traces"`
	Returned int        `json:"returned"`
	// Truncated follows the store's own cursor: it means "more matched than
	// you are reading", which is the fact that matters. There is deliberately
	// no total — counting the matches would be a query the screens never run,
	// and inventing a read path for this client is the one thing this design
	// refuses to do.
	Truncated bool `json:"truncated"`
}

func (p searchTracesPayload) rows() int { return p.Returned }

// enumOr validates a closed-set argument. An invented value is refused rather
// than ignored, for the reason every unknown argument is: a silently dropped
// filter reads back as a filter that was applied.
func enumOr(value, name string, allowed ...string) error {
	if value == "" {
		return nil
	}
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return &toolError{Message: fmt.Sprintf("%s must be one of %s, got %q", name, strings.Join(allowed, ", "), value)}
}

func runSearchTraces(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a searchTracesArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	if err := enumOr(a.Status, "status", "ok", "error"); err != nil {
		return nil, err
	}
	if err := enumOr(a.Order, "order", "newest", "oldest", "slowest"); err != nil {
		return nil, err
	}
	service := ""
	if a.Service != "" {
		if service, err = s.resolveService(ctx, tr, a.Service); err != nil {
			return nil, err
		}
	}
	limit := clampRows(a.Limit, defaultRows, maxRows)
	page, err := s.Store.SearchTraces(ctx, storage.TraceQuery{
		Tenant:      s.Tenant,
		Tenants:     s.Tenants,
		Range:       tr,
		Service:     service,
		Operation:   a.Operation,
		Status:      a.Status,
		Order:       a.Order,
		MinDuration: time.Duration(a.MinDurationMs * float64(time.Millisecond)),
		ExcludeAux:  true,
		Limit:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("searching traces: %w", err)
	}
	rows := make([]traceRow, 0, len(page.Traces))
	for _, t := range page.Traces {
		rows = append(rows, traceRow{
			TraceID: t.TraceID, RootService: t.RootService, RootOperation: t.RootOperation,
			StartTime: t.StartTime.UTC().Format(time.RFC3339), DurationMs: ms(t.Duration),
			SpanCount: t.SpanCount, ErrorCount: t.ErrorCount,
			StatusCode: t.StatusCode, HTTPStatus: t.HTTPStatus,
		})
	}
	return searchTracesPayload{
		Window: toWindowDTO(tr), Traces: rows,
		Returned: len(rows), Truncated: page.NextCursor != nil,
	}, nil
}

var getTraceDef = toolDef{
	Name: "get_trace",
	Description: "Fetch one trace by id: its spans (id, parent, service, operation, kind, duration, status) " +
		"plus a per-service rollup of SELF time — the time actually spent inside each service rather than " +
		"waiting on something it called. Use it to find which hop in a request is the slow or failing one.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: map[string]property{
			"trace_id": {Type: "string", Description: "The trace id, as returned by search_traces."},
			"limit":    {Type: "integer", Description: "Maximum spans (default and maximum 500)."},
		},
		Required: []string{"trace_id"},
	},
}

type getTraceArgs struct {
	TraceID string `json:"trace_id"`
	Limit   int    `json:"limit,omitempty"`
}

type spanRow struct {
	SpanID        string  `json:"spanId"`
	ParentSpanID  string  `json:"parentSpanId,omitempty"`
	Service       string  `json:"service"`
	Operation     string  `json:"operation"`
	Kind          string  `json:"kind,omitempty"`
	StartTime     string  `json:"startTime"`
	DurationMs    float64 `json:"durationMs"`
	StatusCode    string  `json:"statusCode,omitempty"`
	StatusMessage string  `json:"statusMessage,omitempty"`
}

type serviceSelfTime struct {
	Service    string  `json:"service"`
	SelfTimeMs float64 `json:"selfTimeMs"`
	SpanCount  int     `json:"spanCount"`
	ErrorCount int     `json:"errorCount"`
}

type getTracePayload struct {
	TraceID   string            `json:"traceId"`
	Spans     []spanRow         `json:"spans"`
	Services  []serviceSelfTime `json:"services"`
	Returned  int               `json:"returned"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
}

func (p getTracePayload) rows() int { return p.Returned }

func runGetTrace(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a getTraceArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.TraceID) == "" {
		return nil, &toolError{Message: "trace_id is required"}
	}
	trace, err := s.Store.GetTrace(ctx, s.Tenants, a.TraceID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, &toolError{Message: fmt.Sprintf(
			"no trace %q in this project — it may have aged out of retention, or belong to another project", a.TraceID)}
	}
	if err != nil {
		return nil, fmt.Errorf("reading trace: %w", err)
	}

	// The rollup is computed over EVERY span, before the page is cut. Bounding
	// what an agent reads must not quietly change what the totals mean.
	services := selfTimeByService(trace.Spans)

	spans := make([]spanRow, 0, len(trace.Spans))
	for _, sp := range trace.Spans {
		spans = append(spans, spanRow{
			SpanID: sp.SpanID, ParentSpanID: sp.ParentSpanID,
			Service: sp.Service, Operation: sp.Operation, Kind: sp.Kind,
			StartTime: sp.StartTime.UTC().Format(time.RFC3339Nano), DurationMs: ms(sp.Duration),
			StatusCode: sp.StatusCode, StatusMessage: sp.StatusMessage,
		})
	}
	// Chronological, so a truncated page keeps the beginning of the request —
	// the root and its first hops, which is where an investigation starts.
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].StartTime != spans[j].StartTime {
			return spans[i].StartTime < spans[j].StartTime
		}
		return spans[i].SpanID < spans[j].SpanID
	})
	total := len(spans)
	limit := clampRows(a.Limit, maxSpans, maxSpans)
	truncated := total > limit
	if truncated {
		spans = spans[:limit]
	}
	return getTracePayload{
		TraceID: trace.TraceID, Spans: spans, Services: services,
		Returned: len(spans), Total: total, Truncated: truncated,
	}, nil
}

// selfTimeByService weights a trace by where the time actually WENT: each
// span's own duration minus what it spent waiting on its direct children,
// rolled up per service.
//
// This arithmetic already exists once, in the browser
// (ui/src/components/traces/views/trace-path.tsx), where the Path view
// computes it client-side — so the hub has nothing to hand over. Computing it
// here rather than moving the UI's copy is a deliberate cost: unifying them
// means touching a shipped screen, which would put a UI regression in the
// blast radius of a change that otherwise adds nothing to any existing
// surface. The follow-up — the screen reading this number instead — is a
// separate and much safer change.
func selfTimeByService(spans []storage.Span) []serviceSelfTime {
	childTime := make(map[string]time.Duration, len(spans))
	for _, sp := range spans {
		if sp.ParentSpanID != "" {
			childTime[sp.ParentSpanID] += sp.Duration
		}
	}
	agg := make(map[string]*serviceSelfTime, 8)
	for _, sp := range spans {
		self := sp.Duration - childTime[sp.SpanID]
		if self < 0 {
			// Concurrent children can outlast their parent's own clock. Zero,
			// not a negative: "this service waited" is the truth, and a
			// negative number would corrupt the rollup it lands in.
			self = 0
		}
		row, ok := agg[sp.Service]
		if !ok {
			row = &serviceSelfTime{Service: sp.Service}
			agg[sp.Service] = row
		}
		row.SelfTimeMs += ms(self)
		row.SpanCount++
		if strings.EqualFold(sp.StatusCode, "error") {
			row.ErrorCount++
		}
	}
	out := make([]serviceSelfTime, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	// Biggest first: "where did the time go" is answered by the first row.
	sort.Slice(out, func(i, j int) bool {
		if out[i].SelfTimeMs != out[j].SelfTimeMs {
			return out[i].SelfTimeMs > out[j].SelfTimeMs
		}
		return out[i].Service < out[j].Service
	})
	return out
}
```

- [ ] **Step 4: Register them**

In `hub/internal/mcp/tools.go`, extend the registry:

```go
	return []tool{
		{def: listServicesDef, run: runListServices},
		{def: searchTracesDef, run: runSearchTraces},
		{def: getTraceDef, run: runGetTrace},
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd hub && go test ./internal/... -run 'TestSearchTraces|TestGetTrace' -v
cd hub && go test ./...
```

Expected: PASS on both, and every other package unchanged — nothing outside `internal/mcp` was touched.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/mcp/signals.go hub/internal/mcp/signals_test.go hub/internal/mcp/tools.go
git commit -m "feat(hub): mcp search_traces and get_trace

get_trace carries the per-service self time, computed in the hub for this
tool and deliberately NOT taken from the Path view's client-side copy:
unifying them means touching a shipped screen, and this change otherwise
adds nothing to any existing surface. The rollup is over every span, not
the page — bounding what an agent reads must not change what the totals
mean."
```

---

### Task 8: `search_logs` and `list_error_issues` — the two module-gated tools

**Files:**
- Modify: `hub/internal/mcp/signals.go` (append)
- Modify: `hub/internal/mcp/tools.go` (two registry entries, both carrying a module)
- Modify: `hub/internal/mcp/signals_test.go` (append)
- Modify: `hub/internal/mcp/tools_test.go` → create it

- [ ] **Step 1: Write the failing tests**

Create `hub/internal/mcp/tools_test.go`:

```go
package mcp

import (
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

func toolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	got := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	res, _ := got["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		def, _ := tl.(map[string]any)
		names[def["name"].(string)] = true
	}
	return names
}

// The capabilities rule, and the reason it matters more here than in the
// sidebar: a tool that EXISTS will be called. A model that calls one and gets
// a failure reads the failure as a fact about the estate — "this install has
// no logs" becomes "this service logs nothing" — and reasons on from there.
// So a disabled module's tool is absent, not present-and-always-failing.
func TestToolsListHidesDisabledModules(t *testing.T) {
	core, err := modules.Parse("core")
	if err != nil {
		t.Fatal(err)
	}
	s := serverWith(fakeWithServices("a"))
	s.Modules = core

	names := toolNames(t, s)
	for _, want := range []string{"service_context", "list_services", "search_traces", "get_trace"} {
		if !names[want] {
			t.Errorf("%s must always be listed — it reads core trace data", want)
		}
	}
	for _, gone := range []string{"search_logs", "list_error_issues"} {
		if names[gone] {
			t.Errorf("%s listed with its module off", gone)
		}
	}
}

func TestToolsListShowsEverythingWhenEveryModuleRuns(t *testing.T) {
	names := toolNames(t, serverWith(fakeWithServices("a")))
	for _, want := range []string{"service_context", "list_services", "search_traces", "get_trace", "search_logs", "list_error_issues"} {
		if !names[want] {
			t.Errorf("%s missing from tools/list", want)
		}
	}
	if len(names) != 6 {
		t.Errorf("tools/list has %d tools, want exactly 6: %v", len(names), names)
	}
}

// Calling a disabled module's tool by name anyway is invalid-params naming the
// module, not method-not-found: the tool exists, this install does not run it,
// and an agent that is told which module is off can report that instead of
// concluding the signal does not exist.
func TestCallingADisabledToolNamesTheModule(t *testing.T) {
	core, err := modules.Parse("core")
	if err != nil {
		t.Fatal(err)
	}
	s := serverWith(fakeWithServices("a"))
	s.Modules = core

	got := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_logs","arguments":{}}}`)
	e, ok := got["error"].(map[string]any)
	if !ok || e["code"] != float64(codeInvalidParams) {
		t.Fatalf("got %v, want an invalid-params error", got)
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, "logs") {
		t.Errorf("message %q does not name the missing module", msg)
	}
}
```

Add `"strings"` to that file's imports.

Append to `hub/internal/mcp/signals_test.go`:

```go
func TestSearchLogs(t *testing.T) {
	f := fakeWithServices("payment-api")
	f.LogPage = storage.LogPage{Logs: []storage.LogRecord{{
		Timestamp: testNow, Severity: "ERROR", Service: "payment-api",
		Body: "connection refused talking to ledger", TraceID: "abc", SpanID: "s1",
	}}}
	payload, isErr := callTool(t, serverWith(f), "search_logs",
		`{"service":"payment-api","level":"ERROR","query":"refused","window":"30m"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	logs, _ := payload["logs"].([]any)
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want 1: %v", len(logs), payload)
	}
	first, _ := logs[0].(map[string]any)
	// The body is returned in full. Redacting it would make the tool useless
	// for the job it exists to do — the line you would mask is invariably the
	// one that explains the failure. The opt-in module and the audit line are
	// what carry that decision (design/2026-09-01-mcp-server.md).
	if first["body"] != "connection refused talking to ledger" {
		t.Errorf("body = %v", first["body"])
	}
	if first["traceId"] != "abc" {
		t.Errorf("traceId = %v — correlation is the point of returning logs here", first["traceId"])
	}
	if f.LastLogQuery.MinSeverity != "ERROR" || f.LastLogQuery.Query != "refused" {
		t.Errorf("filters did not reach storage: %+v", f.LastLogQuery)
	}
}

// A trace id takes the correlated path, not the search path: it is a different
// store call, and the one that answers "what did this request log".
func TestSearchLogsByTraceID(t *testing.T) {
	f := fakeWithServices("payment-api")
	f.TraceLogs = map[string][]storage.LogRecord{"abc": {{
		Timestamp: testNow, Severity: "ERROR", Service: "payment-api", Body: "boom", TraceID: "abc"}}}
	payload, isErr := callTool(t, serverWith(f), "search_logs", `{"trace_id":"abc"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	logs, _ := payload["logs"].([]any)
	if len(logs) != 1 {
		t.Fatalf("got %d logs, want the trace's 1: %v", len(logs), payload)
	}
}

func TestListErrorIssues(t *testing.T) {
	f := fakeWithServices("payment-api")
	f.Issues = []storage.ErrorIssue{{
		Fingerprint: 0xdeadbeef, Service: "payment-api", Type: "ConnectionError",
		Message: "connection refused", Source: "span", Status: "unresolved",
		Count: 42, FirstSeen: testNow.Add(-time.Hour), LastSeen: testNow, LastTraceID: "abc",
	}}
	payload, isErr := callTool(t, serverWith(f), "list_error_issues", `{"service":"payment-api"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	issues, _ := payload["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1: %v", len(issues), payload)
	}
	first, _ := issues[0].(map[string]any)
	if first["type"] != "ConnectionError" || first["count"] != float64(42) {
		t.Errorf("issue row = %v", first)
	}
	// Hex, the same wire form the REST API uses, so an id an agent reads here
	// pastes into a URL a human can open.
	if first["fingerprint"] != "00000000deadbeef" {
		t.Errorf("fingerprint = %v, want the hex form", first["fingerprint"])
	}
	if first["lastTraceId"] != "abc" {
		t.Errorf("lastTraceId = %v — it is the bridge to get_trace", first["lastTraceId"])
	}
	// Open issues by default: an agent asking what is broken is not asking
	// what used to be.
	if f.LastIssueQuery.Status != "unresolved" {
		t.Errorf("default status = %q, want unresolved", f.LastIssueQuery.Status)
	}
}
```

`LastLogQuery` and `LastIssueQuery` already exist on the fake and are already assigned — nothing to add there either.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd hub && go test ./internal/mcp/... -run 'TestSearchLogs|TestListErrorIssues|TestToolsList|TestCallingADisabled'
```

Expected: FAIL — `unknown tool "search_logs"`, `unknown tool "list_error_issues"`, and `TestToolsListShowsEverythingWhenEveryModuleRuns` reporting 3 tools where it wants 6.

- [ ] **Step 3: Write the two tools**

Append to `hub/internal/mcp/signals.go`:

```go
var searchLogsDef = toolDef{
	Name: "search_logs",
	Description: "Search log records: by service, minimum severity, a substring of the message, or a trace id. " +
		"Passing trace_id returns exactly the logs correlated to that request, which is how you read what one " +
		"failing request actually printed. Log bodies are returned in full.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"service":  {Type: "string", Description: "Only logs from this service."},
			"level":    {Type: "string", Description: `Minimum severity, e.g. "WARN" or "ERROR". Matches that level and worse.`},
			"query":    {Type: "string", Description: "Case-insensitive substring of the log body."},
			"trace_id": {Type: "string", Description: "Return the logs correlated to this trace. Ignores the other filters and the window."},
			"limit":    {Type: "integer", Description: "Maximum rows (default 20, maximum 100)."},
		}),
	},
}

type searchLogsArgs struct {
	windowArgs
	Service string `json:"service,omitempty"`
	Level   string `json:"level,omitempty"`
	Query   string `json:"query,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type logRow struct {
	Timestamp string `json:"timestamp"`
	Severity  string `json:"severity"`
	Service   string `json:"service"`
	Body      string `json:"body"`
	TraceID   string `json:"traceId,omitempty"`
	SpanID    string `json:"spanId,omitempty"`
}

func toLogRow(l storage.LogRecord) logRow {
	return logRow{
		Timestamp: l.Timestamp.UTC().Format(time.RFC3339Nano), Severity: l.Severity,
		Service: l.Service, Body: l.Body, TraceID: l.TraceID, SpanID: l.SpanID,
	}
}

type searchLogsPayload struct {
	Window    *windowDTO `json:"window,omitempty"` // absent on a trace_id read, which has no window
	Logs      []logRow   `json:"logs"`
	Returned  int        `json:"returned"`
	Truncated bool       `json:"truncated"`
}

func (p searchLogsPayload) rows() int { return p.Returned }

func runSearchLogs(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a searchLogsArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	limit := clampRows(a.Limit, defaultRows, maxRows)

	// A trace id takes the correlated path — a different store call, and the
	// one that answers "what did THIS request print". Mixing it into the
	// search would return whatever else happened to be in the window too.
	if a.TraceID != "" {
		records, err := s.Store.LogsForTrace(ctx, s.Tenants, a.TraceID)
		if err != nil {
			return nil, fmt.Errorf("reading logs for trace: %w", err)
		}
		rows := make([]logRow, 0, len(records))
		for _, l := range records {
			rows = append(rows, toLogRow(l))
		}
		truncated := len(rows) > limit
		if truncated {
			rows = rows[:limit]
		}
		return searchLogsPayload{Logs: rows, Returned: len(rows), Truncated: truncated}, nil
	}

	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	service := ""
	if a.Service != "" {
		if service, err = s.resolveService(ctx, tr, a.Service); err != nil {
			return nil, err
		}
	}
	page, err := s.Store.SearchLogs(ctx, storage.LogQuery{
		Tenant:      s.Tenant,
		Tenants:     s.Tenants,
		Range:       tr,
		Service:     service,
		MinSeverity: a.Level,
		Query:       a.Query,
		Limit:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("searching logs: %w", err)
	}
	rows := make([]logRow, 0, len(page.Logs))
	for _, l := range page.Logs {
		rows = append(rows, toLogRow(l))
	}
	w := toWindowDTO(tr)
	return searchLogsPayload{
		Window: &w, Logs: rows,
		Returned: len(rows), Truncated: page.NextCursor != nil,
	}, nil
}

var listErrorIssuesDef = toolDef{
	Name: "list_error_issues",
	Description: "List deduplicated error issues — exceptions grouped by fingerprint, with first/last seen and an " +
		"occurrence count — rather than raw exception events. Unresolved by default. Each issue carries the id of a " +
		"trace it last occurred in, which get_trace will open.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"service": {Type: "string", Description: "Only issues from this service."},
			"query":   {Type: "string", Description: "Case-insensitive substring of the exception type or message."},
			"status":  {Type: "string", Description: `Triage state; "unresolved" by default.`, Enum: []string{"unresolved", "resolved", "ignored", "all"}},
			"sort":    {Type: "string", Description: `"lastSeen" (default), "count" or "firstSeen".`, Enum: []string{"lastSeen", "count", "firstSeen"}},
			"limit":   {Type: "integer", Description: "Maximum rows (default 20, maximum 100)."},
		}),
	},
}

type listErrorIssuesArgs struct {
	windowArgs
	Service string `json:"service,omitempty"`
	Query   string `json:"query,omitempty"`
	Status  string `json:"status,omitempty"`
	Sort    string `json:"sort,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type issueRow struct {
	Fingerprint string `json:"fingerprint"`
	Service     string `json:"service"`
	Type        string `json:"type"`
	Message     string `json:"message"`
	Source      string `json:"source,omitempty"`
	Status      string `json:"status"`
	Regressed   bool   `json:"regressed,omitempty"`
	Count       uint64 `json:"count"`
	FirstSeen   string `json:"firstSeen"`
	LastSeen    string `json:"lastSeen"`
	LastTraceID string `json:"lastTraceId,omitempty"`
}

func toIssueRow(i storage.ErrorIssue) issueRow {
	return issueRow{
		// Hex, the same wire form the REST API uses (fingerprintHex), so an id
		// an agent reports pastes straight into a URL a human can open.
		Fingerprint: fmt.Sprintf("%016x", i.Fingerprint),
		Service:     i.Service, Type: i.Type, Message: i.Message, Source: i.Source,
		Status: i.Status, Regressed: i.Regressed, Count: i.Count,
		FirstSeen: i.FirstSeen.UTC().Format(time.RFC3339),
		LastSeen:  i.LastSeen.UTC().Format(time.RFC3339),
		// The bridge to get_trace: an issue an agent can only read about is
		// half an answer.
		LastTraceID: i.LastTraceID,
	}
}

type listErrorIssuesPayload struct {
	Window    windowDTO  `json:"window"`
	Issues    []issueRow `json:"issues"`
	Returned  int        `json:"returned"`
	Truncated bool       `json:"truncated"`
}

func (p listErrorIssuesPayload) rows() int { return p.Returned }

func runListErrorIssues(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a listErrorIssuesArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	if err := enumOr(a.Status, "status", "unresolved", "resolved", "ignored", "all"); err != nil {
		return nil, err
	}
	if err := enumOr(a.Sort, "sort", "lastSeen", "count", "firstSeen"); err != nil {
		return nil, err
	}
	service := ""
	if a.Service != "" {
		if service, err = s.resolveService(ctx, tr, a.Service); err != nil {
			return nil, err
		}
	}
	status := a.Status
	if status == "" {
		// The REST API's empty status means "every state". This tool defaults
		// to unresolved instead, deliberately and in its description: an agent
		// asking what is broken is not asking what used to be, and a page of
		// long-resolved issues is how an investigation goes down a dead end.
		status = "unresolved"
	}
	limit := clampRows(a.Limit, defaultRows, maxRows)
	issues, err := s.Store.SearchErrorIssues(ctx, storage.ErrorIssueQuery{
		Tenant:  s.Tenant,
		Tenants: s.Tenants,
		Range:   tr,
		Status:  status,
		Service: service,
		Query:   a.Query,
		Sort:    a.Sort,
		// One more than asked for, so "there is more" is a fact and not a
		// guess: this store call returns a slice, not a cursor.
		Limit: limit + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("searching error issues: %w", err)
	}
	truncated := len(issues) > limit
	if truncated {
		issues = issues[:limit]
	}
	rows := make([]issueRow, 0, len(issues))
	for _, i := range issues {
		rows = append(rows, toIssueRow(i))
	}
	return listErrorIssuesPayload{
		Window: toWindowDTO(tr), Issues: rows,
		Returned: len(rows), Truncated: truncated,
	}, nil
}
```

- [ ] **Step 4: Register them, with their modules**

In `hub/internal/mcp/tools.go`:

```go
	return []tool{
		{def: listServicesDef, run: runListServices},
		{def: searchTracesDef, run: runSearchTraces},
		{def: getTraceDef, run: runGetTrace},
		{def: searchLogsDef, module: modules.Logs, run: runSearchLogs},
		{def: listErrorIssuesDef, module: modules.ErrorTracking, run: runListErrorIssues},
	}
```

`TestToolsListShowsEverythingWhenEveryModuleRuns` wants six and this is five; `service_context` is Task 9 and completes it. Until then that one assertion fails on the count — expected, and named in the next step.

- [ ] **Step 5: Run the tests**

```bash
cd hub && go test ./internal/mcp/... -run 'TestSearchLogs|TestListErrorIssues|TestToolsListHides|TestCallingADisabled' -v
cd hub && go test ./internal/storage/... ./internal/api/...
```

Expected: PASS on all of those. `TestToolsListShowsEverythingWhenEveryModuleRuns` is the one known red test at this point (5 of 6 tools) and goes green in Task 9.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/mcp/signals.go hub/internal/mcp/signals_test.go hub/internal/mcp/tools.go hub/internal/mcp/tools_test.go
git commit -m "feat(hub): mcp search_logs and list_error_issues

The two tools whose modules an install may not run, and the rule that
comes with them: a disabled module's tool is ABSENT from tools/list, not
present and always failing. A tool that exists gets called, and a model
reads the failure as a fact about the estate.

Log bodies are returned in full. Redacting them would make the tool
useless for the job it exists to do — the line you would mask is
invariably the one that explains the failure — so the opt-in module and
the audit line carry that decision instead."
```

---

### Task 9: `service_context` — the composite entry point

**Files:**
- Create: `hub/internal/mcp/context.go`
- Create: `hub/internal/mcp/context_test.go`
- Modify: `hub/internal/mcp/args.go` (refactor `resolveService` onto a stats-returning core)
- Modify: `hub/internal/mcp/protocol.go` (one new `Server` field)
- Modify: `hub/internal/mcp/tools.go` (the registry entry, first)
- Modify: `hub/internal/api/mcp.go` (pass the topology classifier)

- [ ] **Step 1: Write the failing test**

Create `hub/internal/mcp/context_test.go`:

```go
package mcp

import (
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

func contextFake() *storagetest.Fake {
	f := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "payment-api", SpanCount: 3600, ErrorCount: 360, P50: 10 * time.Millisecond,
				P95: 220 * time.Millisecond, P99: 900 * time.Millisecond},
			{Name: "frontend", SpanCount: 7200},
			{Name: "ledger", SpanCount: 3600, ErrorCount: 360},
		},
		Edges: []storage.ServiceEdge{
			{Source: "frontend", Target: "payment-api", Count: 3600, ErrorCount: 360, P95: 240 * time.Millisecond},
			{Source: "payment-api", Target: "ledger", Count: 3600, ErrorCount: 360, P95: 200 * time.Millisecond},
			{Source: "frontend", Target: "ledger", Count: 10}, // not this service's business
		},
		Issues: []storage.ErrorIssue{{
			Fingerprint: 1, Service: "payment-api", Type: "ConnectionError",
			Message: "connection refused", Status: "unresolved", Count: 360,
			FirstSeen: testNow.Add(-time.Hour), LastSeen: testNow, LastTraceID: "abc",
		}},
		AlertStates: []storage.AlertState{
			{Tenant: "default", RuleName: "payments-degraded", Target: "payments", Status: "firing", Since: testNow.Add(-10 * time.Minute)},
			{Tenant: "default", RuleName: "quiet", Target: "cart", Status: "ok"},
		},
	}
	return f
}

// The whole reason this tool exists: the picture a human starts with, in one
// call. Given a bare tool per endpoint a model opens an investigation by
// guessing, and spends five round trips assembling what one query knows.
func TestServiceContextAnswersInOneCall(t *testing.T) {
	payload, isErr := callTool(t, serverWith(contextFake()), "service_context", `{"service":"payment-api","window":"1h"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	if payload["service"] != "payment-api" {
		t.Fatalf("service = %v", payload["service"])
	}
	red, _ := payload["red"].(map[string]any)
	if red["errorRate"] != 0.1 {
		t.Errorf("errorRate = %v, want 0.1", red["errorRate"])
	}
	if red["ratePerSec"] != float64(1) {
		t.Errorf("ratePerSec = %v, want 1 (3600 over an hour)", red["ratePerSec"])
	}
	if red["p95Ms"] != float64(220) {
		t.Errorf("p95Ms = %v, want 220", red["p95Ms"])
	}

	callers, _ := payload["callers"].([]any)
	if len(callers) != 1 {
		t.Fatalf("got %d callers, want 1: %v", len(callers), callers)
	}
	if c, _ := callers[0].(map[string]any); c["service"] != "frontend" || c["p95Ms"] != float64(240) {
		t.Errorf("caller = %v, want frontend at 240ms client-side", c)
	}

	deps, _ := payload["dependencies"].([]any)
	if len(deps) != 1 {
		t.Fatalf("got %d dependencies, want 1: %v", len(deps), deps)
	}
	if d, _ := deps[0].(map[string]any); d["service"] != "ledger" || d["errorRate"] != 0.1 {
		t.Errorf("dependency = %v, want ledger at a 10%% error rate", d)
	}

	issues, _ := payload["topIssues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	alerts, _ := payload["firingAlerts"].([]any)
	if len(alerts) != 1 {
		t.Fatalf("got %d firing alerts, want 1 (the resolved one is not firing)", len(alerts))
	}
	if a, _ := alerts[0].(map[string]any); a["rule"] != "payments-degraded" {
		t.Errorf("alert = %v", a)
	}
}

// A section that is missing because a module is off must SAY so. Silence reads
// as "there are no issues", which is a different and much worse claim.
func TestServiceContextNamesWhatItCouldNotRead(t *testing.T) {
	core, err := modules.Parse("core")
	if err != nil {
		t.Fatal(err)
	}
	s := serverWith(contextFake())
	s.Modules = core

	payload, isErr := callTool(t, s, "service_context", `{"service":"payment-api"}`)
	if isErr {
		t.Fatalf("unexpected tool error: %v", payload)
	}
	if _, present := payload["topIssues"]; present {
		t.Error("topIssues present with error-tracking off — absent is the honest shape")
	}
	notes, _ := payload["notes"].([]any)
	if len(notes) == 0 {
		t.Fatal("no notes: a module that is off has to be named, not silently omitted")
	}
	joined := fmt.Sprint(notes...)
	for _, want := range []string{"error-tracking", "alerting", "logs"} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes do not mention %q: %v", want, notes)
		}
	}
}

// A mesh proxy on a dependency list is a claimed relationship between services
// that never talk to each other. It is reported with its role rather than
// dropped: dropping it on a meshed install leaves an empty list, which reads
// as "this service depends on nothing".
func TestServiceContextLabelsTransportNeighbours(t *testing.T) {
	f := contextFake()
	f.Services = append(f.Services, storage.ServiceStats{Name: "istio-ingressgateway", SpanCount: 7200})
	f.Edges = append(f.Edges, storage.ServiceEdge{Source: "istio-ingressgateway", Target: "payment-api", Count: 100})
	s := serverWith(f)
	s.Topology = topology.New(topology.Default())

	payload, _ := callTool(t, s, "service_context", `{"service":"payment-api"}`)
	callers, _ := payload["callers"].([]any)
	var gateway map[string]any
	for _, c := range callers {
		row, _ := c.(map[string]any)
		if row["service"] == "istio-ingressgateway" {
			gateway = row
		}
	}
	if gateway == nil {
		t.Fatalf("the gateway is missing from callers: %v", callers)
	}
	if gateway["role"] != "transport" {
		t.Errorf("role = %v, want transport", gateway["role"])
	}
}

func TestServiceContextUnknownService(t *testing.T) {
	payload, isErr := callTool(t, serverWith(contextFake()), "service_context", `{"service":"payment_api"}`)
	if !isErr {
		t.Fatalf("unknown service accepted: %v", payload)
	}
	hints, _ := payload["didYouMean"].([]any)
	if len(hints) == 0 {
		t.Errorf("no near matches offered: %v", payload)
	}
}
```

Add the imports that file needs: `fmt`, `strings`, and `github.com/avuru/avuru-obs/hub/internal/storage/storagetest`.

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd hub && go test ./internal/mcp/... -run TestServiceContext
```

Expected: FAIL — `unknown tool "service_context"`, and a compile error on `s.Topology` (the field does not exist yet).

- [ ] **Step 3: Add the classifier to the Server**

In `hub/internal/mcp/protocol.go`, add to `Server` after `Tenants`, and add the `topology` import:

```go
	// Topology is the transport classifier — which workload names are mesh
	// proxies and gateways rather than applications. It comes from the API,
	// built from the SAME hot-reloaded config the service map uses, so the map
	// and this tool cannot disagree about what a proxy is. The zero value
	// classifies nothing as transport, which is what an unmeshed install and a
	// test both want.
	Topology topology.Classifier
```

- [ ] **Step 4: Refactor `resolveService` onto a stats-returning core**

In `hub/internal/mcp/args.go`, replace `resolveService` with these two functions — the matching logic is unchanged, it just also hands back what it found, so the composite tool does not read the service list twice to answer one question:

```go
// resolveService turns the name an agent asked for into one this estate knows.
func (s *Server) resolveService(ctx context.Context, tr storage.TimeRange, name string) (string, error) {
	stats, _, err := s.resolveServiceStats(ctx, tr, name)
	if err != nil {
		return "", err
	}
	return stats.Name, nil
}

// resolveServiceStats is resolveService plus the row it matched and the full
// service list, both of which service_context needs anyway.
//
// Returning no rows for an unknown name is the cheaper answer and the
// dangerous one: a model handed an empty list for a misspelling reads it as
// "this service is dead" and says so with confidence. Naming the near matches
// turns a dead end into the next question.
func (s *Server) resolveServiceStats(ctx context.Context, tr storage.TimeRange, name string) (storage.ServiceStats, []storage.ServiceStats, error) {
	if strings.TrimSpace(name) == "" {
		return storage.ServiceStats{}, nil, &toolError{Message: "service is required"}
	}
	all, err := s.Store.ListServices(ctx, s.serviceQuery(tr))
	if err != nil {
		return storage.ServiceStats{}, nil, fmt.Errorf("listing services: %w", err)
	}
	known := make([]string, 0, len(all))
	for _, svc := range all {
		if svc.Name == name {
			return svc, all, nil
		}
		known = append(known, svc.Name)
	}
	for _, svc := range all {
		if strings.EqualFold(svc.Name, name) {
			return svc, all, nil // the stored spelling wins — it is what filters match
		}
	}
	return storage.ServiceStats{}, all, &toolError{
		Message: fmt.Sprintf("no service named %q reported anything between %s and %s",
			name, tr.Start.Format(time.RFC3339), tr.End.Format(time.RFC3339)),
		DidYouMean: nearest(name, known, 5),
	}
}
```

- [ ] **Step 5: Write the tool**

Create `hub/internal/mcp/context.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

var serviceContextDef = toolDef{
	Name: "service_context",
	Description: "Everything known about one service in a window, in a single call: request rate, error rate and " +
		"latency percentiles; who calls it and what it depends on, each with call rate, error rate and client-side " +
		"p95; its open error issues; and any alerts firing in this project. START HERE when investigating a " +
		"service, then drill down with search_traces, search_logs or get_trace.",
	InputSchema: inputSchema{
		Type: "object",
		Properties: withWindow(map[string]property{
			"service": {Type: "string", Description: "The service to describe."},
		}),
		Required: []string{"service"},
	},
}

type serviceContextArgs struct {
	windowArgs
	Service string `json:"service"`
}

type neighbourRow struct {
	Service string `json:"service"`
	// Role is "transport" for a mesh proxy or gateway, absent for an
	// application. A proxy is reported rather than hidden: on a meshed install
	// dropping it would leave an empty list, which reads as "this service
	// depends on nothing".
	Role        string  `json:"role,omitempty"`
	Calls       uint64  `json:"calls"`
	CallsPerSec float64 `json:"callsPerSec"`
	ErrorRate   float64 `json:"errorRate"`
	// Client-side p95: what the CALLER experienced on this path, which is what
	// makes a single slow route into an otherwise healthy service visible.
	P95Ms float64 `json:"p95Ms,omitempty"`
}

type alertRow struct {
	Rule   string `json:"rule"`
	Target string `json:"target"`
	Since  string `json:"since"`
}

type serviceContextPayload struct {
	Service      string         `json:"service"`
	Window       windowDTO      `json:"window"`
	RED          serviceRow     `json:"red"`
	Callers      []neighbourRow `json:"callers"`
	Dependencies []neighbourRow `json:"dependencies"`
	// Absent — not empty — when the module that owns them is off. An empty
	// list is a claim ("no issues"); absence plus a note is the truth.
	TopIssues    []issueRow `json:"topIssues,omitempty"`
	FiringAlerts []alertRow `json:"firingAlerts,omitempty"`
	// Notes names what this install could not answer and why. Without it a
	// model reads a missing section as an absence of trouble, which is the
	// same way of being confidently wrong that v0.11's "reported no usage"
	// bucket exists to prevent.
	Notes    []string `json:"notes,omitempty"`
	Returned int      `json:"returned"`
}

func (p serviceContextPayload) rows() int { return p.Returned }

func runServiceContext(ctx context.Context, s *Server, raw json.RawMessage) (any, error) {
	var a serviceContextArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	tr, err := a.timeRange(s.now())
	if err != nil {
		return nil, err
	}
	stats, all, err := s.resolveServiceStats(ctx, tr, a.Service)
	if err != nil {
		return nil, err
	}
	name := stats.Name

	edges, err := s.Store.ServiceEdges(ctx, s.serviceQuery(tr))
	if err != nil {
		return nil, fmt.Errorf("reading service edges: %w", err)
	}
	// One classifier for the whole answer, carrying the mesh's own label
	// evidence, exactly as the service map builds it — so the two cannot end
	// up disagreeing about which workload is a proxy.
	cls := s.Topology.WithEvidence(labelledTransport(all))

	payload := serviceContextPayload{
		Service:      name,
		Window:       toWindowDTO(tr),
		RED:          toServiceRow(stats, tr),
		Callers:      neighbours(edges, name, tr, cls, inbound),
		Dependencies: neighbours(edges, name, tr, cls, outbound),
	}

	if s.Modules.Enabled(modules.ErrorTracking) {
		issues, err := s.Store.SearchErrorIssues(ctx, storage.ErrorIssueQuery{
			Tenant: s.Tenant, Tenants: s.Tenants, Range: tr,
			Status: "unresolved", Service: name, Sort: "count", Limit: 5,
		})
		if err != nil {
			return nil, fmt.Errorf("reading error issues: %w", err)
		}
		payload.TopIssues = make([]issueRow, 0, len(issues))
		for _, i := range issues {
			payload.TopIssues = append(payload.TopIssues, toIssueRow(i))
		}
	} else {
		payload.Notes = append(payload.Notes,
			"the error-tracking module is not enabled on this install, so topIssues is absent — that is not the same as this service having no errors")
	}

	if s.Modules.Enabled(modules.Alerting) {
		alerts, err := s.firingAlerts(ctx)
		if err != nil {
			return nil, err
		}
		payload.FiringAlerts = alerts
	} else {
		payload.Notes = append(payload.Notes,
			"the alerting module is not enabled on this install, so firingAlerts is absent — nothing here says whether anyone was paged")
	}

	if !s.Modules.Enabled(modules.Logs) {
		payload.Notes = append(payload.Notes,
			"the logs module is not enabled on this install, so search_logs is unavailable")
	}

	payload.Returned = len(payload.Callers) + len(payload.Dependencies) + len(payload.TopIssues) + len(payload.FiringAlerts)
	return payload, nil
}

// firingAlerts returns every alert currently firing in this project, not just
// the ones whose target is this service by name.
//
// A rule targets a service OR a service-health group, so filtering by name
// would silently hide the alert that actually covers this service — the one
// piece of context an investigation most wants. Firing alerts are rare, the
// set is small, and every row carries its target, so the caller can tell.
func (s *Server) firingAlerts(ctx context.Context) ([]alertRow, error) {
	var out []alertRow
	for _, tenant := range s.Tenants {
		states, err := s.Store.LoadAlertStates(ctx, tenant)
		if err != nil {
			return nil, fmt.Errorf("reading alert state: %w", err)
		}
		for _, st := range states {
			if st.Status != "firing" {
				continue
			}
			out = append(out, alertRow{
				Rule: st.RuleName, Target: st.Target,
				Since: st.Since.UTC().Format(time.RFC3339),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since != out[j].Since {
			return out[i].Since < out[j].Since // longest-firing first
		}
		return out[i].Rule < out[j].Rule
	})
	return out, nil
}

// direction selects which end of an edge is the counterpart.
type direction int

const (
	inbound  direction = iota // edges INTO the service: its callers
	outbound                  // edges OUT of it: its dependencies
)

// neighbours builds one side of the dependency picture, summing the edges that
// touch `name` and reporting each counterpart once.
//
// Mesh-hidden hops are NOT recovered here: the service map collapses an
// app→proxy→app path back into the app→app dependency it really is
// (design/2026-08-25-transport-hop-collapse.md), and reimplementing that merge
// for this client would be a second set of semantics to keep in step with the
// first. What this does instead is LABEL a transport counterpart, so an agent
// reading "istio-ingressgateway (transport)" knows it is looking at a hop
// rather than at an application. Bringing the collapse itself here is a
// follow-up, and a safe one.
func neighbours(edges []storage.ServiceEdge, name string, tr storage.TimeRange, cls topology.Classifier, dir direction) []neighbourRow {
	agg := map[string]*storage.ServiceEdge{}
	for i := range edges {
		e := edges[i]
		var other string
		switch dir {
		case inbound:
			if e.Target != name {
				continue
			}
			other = e.Source
		case outbound:
			if e.Source != name {
				continue
			}
			other = e.Target
		}
		if other == "" || other == name {
			continue
		}
		cur, ok := agg[other]
		if !ok {
			cp := e
			agg[other] = &cp
			continue
		}
		cur.Count += e.Count
		cur.ErrorCount += e.ErrorCount
		if e.P95 > cur.P95 {
			cur.P95 = e.P95
		}
	}
	out := make([]neighbourRow, 0, len(agg))
	for other, e := range agg {
		row := neighbourRow{
			Service:     other,
			Calls:       e.Count,
			CallsPerSec: perSec(e.Count, tr),
			ErrorRate:   ratio(e.ErrorCount, e.Count),
			P95Ms:       ms(e.P95),
		}
		if cls.IsTransport(other) {
			row.Role = string(topology.RoleTransport)
		}
		out = append(out, row)
	}
	// Worst first, busiest next — the same order list_services uses, so a
	// reader learns one rule and it holds everywhere.
	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].ErrorRate != out[j].ErrorRate:
			return out[i].ErrorRate > out[j].ErrorRate
		case out[i].Calls != out[j].Calls:
			return out[i].Calls > out[j].Calls
		default:
			return out[i].Service < out[j].Service
		}
	})
	return out
}

// labelledTransport is the subset of services the MESH ITSELF identified, via
// the labels it writes on its own data plane and the sensor carries on their
// spans. Derived from the same rows this answer was built from, so the
// evidence cannot describe a different set of services than the answer does.
func labelledTransport(services []storage.ServiceStats) []string {
	var out []string
	for _, s := range services {
		if s.TransportEvidence {
			out = append(out, s.Name)
		}
	}
	return out
}
```

- [ ] **Step 6: Register it first**

In `hub/internal/mcp/tools.go`, put it at the head of the registry — `tools/list` order is what a model reads first, and this is the tool it should reach for first:

```go
	return []tool{
		{def: serviceContextDef, run: runServiceContext},
		{def: listServicesDef, run: runListServices},
		{def: searchTracesDef, run: runSearchTraces},
		{def: getTraceDef, run: runGetTrace},
		{def: searchLogsDef, module: modules.Logs, run: runSearchLogs},
		{def: listErrorIssuesDef, module: modules.ErrorTracking, run: runListErrorIssues},
	}
```

- [ ] **Step 7: Hand the classifier down from the API**

In `hub/internal/api/mcp.go`, add the field to the `mcp.Server` literal:

```go
	srv := &mcp.Server{
		Store:    store,
		Modules:  a.modules,
		Tenant:   tenant,
		Tenants:  tenants,
		Topology: a.topologyClassifier(),
		Version:  Version,
		Actor:    actorName(r),
	}
```

`a.topologyClassifier()` is the same accessor the service map uses, reading the same hot-reloaded config — which is the point: one source, so the map and the tool cannot disagree about what a proxy is.

- [ ] **Step 8: Run the tests to verify they pass**

```bash
cd hub && go test ./internal/mcp/... -v
cd hub && go test ./... && golangci-lint run
```

Expected: PASS everywhere, including `TestToolsListShowsEverythingWhenEveryModuleRuns` (six tools at last) and every earlier task's tests.

- [ ] **Step 9: Commit**

```bash
git add hub/internal/mcp/context.go hub/internal/mcp/context_test.go hub/internal/mcp/args.go hub/internal/mcp/protocol.go hub/internal/mcp/tools.go hub/internal/api/mcp.go
git commit -m "feat(hub): mcp service_context, the composite entry point

The first call is the one an agent gets wrong. Given a bare tool per
endpoint a model opens an investigation by guessing and spends five round
trips assembling what one query knows; given this, it starts with the
picture a human starts with.

A section its install cannot answer is ABSENT and NAMED in notes, never
an empty list: silence about a module that is off reads as an absence of
trouble."
```

---

### Task 10: The end-to-end case

Proves the whole path against a real stack: an MCP client speaks JSON-RPC to the hub, the hub reads ClickHouse, and the numbers that come back are the ones the fixture seeded.

**Files:**
- Create: `e2e/mcp_test.go`

- [ ] **Step 1: Write the test**

Create `e2e/mcp_test.go`:

```go
//go:build e2e

// The MCP server against a real stack: JSON-RPC in, the seeded fixture out.
// The compose hub sets no AVURUOBS_MODULES, so every module including mcp is
// active and POST /mcp exists — the same reason green and ai are exercised here.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// mcpCall sends one JSON-RPC request as the authenticated admin and returns
// the decoded reply.
func mcpCall(method, params string) (map[string]any, error) {
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `"`
	if params != "" {
		body += `,"params":` + params
	}
	body += "}"
	resp, err := apiClient.Post(hubURL+"/mcp", "application/json", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("POST /mcp %s: %s", method, resp.Status)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if e, ok := out["error"]; ok {
		return nil, fmt.Errorf("%s: %v", method, e)
	}
	return out, nil
}

// mcpTool runs one tool and returns its payload plus whether the tool itself
// reported an error (as opposed to the protocol doing so).
func mcpTool(name, args string) (map[string]any, bool, error) {
	out, err := mcpCall("tools/call", `{"name":"`+name+`","arguments":`+args+`}`)
	if err != nil {
		return nil, false, err
	}
	res, _ := out["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return nil, false, fmt.Errorf("%s: no content in %v", name, res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, false, fmt.Errorf("%s: payload is not JSON: %s", name, text)
	}
	isErr, _ := res["isError"].(bool)
	return payload, isErr, nil
}

func TestMCPHandshakeAndTools(t *testing.T) {
	out, err := mcpCall("initialize", `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}`)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	res, _ := out["result"].(map[string]any)
	if res["protocolVersion"] == nil {
		t.Fatalf("no protocolVersion in %v", res)
	}

	out, err = mcpCall("tools/list", "")
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	res, _ = out["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		def, _ := tl.(map[string]any)
		names[fmt.Sprint(def["name"])] = true
	}
	// Every module runs in the compose stack, so all six must be listed.
	for _, want := range []string{"service_context", "list_services", "search_traces", "get_trace", "search_logs", "list_error_issues"} {
		if !names[want] {
			t.Errorf("tools/list is missing %s: %v", want, names)
		}
	}
}

// The seeded fixture is one trace in seed-checkout: three spans, one of them
// the entry span, and it failed. So the service reports exactly one request at
// a 100% error rate — deterministic, unlike the HotROD traffic beside it.
func TestMCPServiceContextReadsTheSeededService(t *testing.T) {
	poll(t, 30*time.Second, func() error {
		payload, isErr, err := mcpTool("service_context", `{"service":"seed-checkout","window":"2h"}`)
		if err != nil {
			return err
		}
		if isErr {
			return fmt.Errorf("tool error: %v", payload["error"])
		}
		red, _ := payload["red"].(map[string]any)
		if red["spanCount"] != float64(1) {
			return fmt.Errorf("spanCount = %v, want the fixture's 1 entry span", red["spanCount"])
		}
		if red["errorRate"] != float64(1) {
			return fmt.Errorf("errorRate = %v, want 1 — the seeded entry span failed", red["errorRate"])
		}
		return nil
	})
}

func TestMCPGetTraceRollsUpSelfTime(t *testing.T) {
	poll(t, 30*time.Second, func() error {
		payload, isErr, err := mcpTool("get_trace", `{"trace_id":"`+seededTraceID+`"}`)
		if err != nil {
			return err
		}
		if isErr {
			return fmt.Errorf("tool error: %v", payload["error"])
		}
		spans, _ := payload["spans"].([]any)
		if len(spans) != 3 {
			return fmt.Errorf("got %d spans, want the fixture's 3", len(spans))
		}
		services, _ := payload["services"].([]any)
		if len(services) != 1 {
			return fmt.Errorf("got %d services in the rollup, want 1 (every span is seed-checkout)", len(services))
		}
		row, _ := services[0].(map[string]any)
		if row["service"] != "seed-checkout" {
			return fmt.Errorf("rollup service = %v", row["service"])
		}
		// Root 250ms minus its two children: a positive number strictly under
		// the root's own duration is what "self time" has to mean.
		self, _ := row["selfTimeMs"].(float64)
		if self <= 0 || self >= 250 {
			return fmt.Errorf("selfTimeMs = %v, want 0 < self < 250", self)
		}
		return nil
	})
}

// The rule this server turns on, proven against real data: a misspelled name
// is an error naming the near matches, never an empty result a model would
// read as "this service is dead".
func TestMCPUnknownServiceNamesNearMatches(t *testing.T) {
	poll(t, 30*time.Second, func() error {
		payload, isErr, err := mcpTool("service_context", `{"service":"seed-checkut","window":"2h"}`)
		if err != nil {
			return err
		}
		if !isErr {
			return fmt.Errorf("a misspelled service was accepted: %v", payload)
		}
		hints, _ := payload["didYouMean"].([]any)
		for _, h := range hints {
			if h == "seed-checkout" {
				return nil
			}
		}
		return fmt.Errorf("didYouMean = %v, want seed-checkout", hints)
	})
}

// Correlated logs through the tool, on the same trace the REST suite reads:
// the seeded fixture puts 2 log records on it.
func TestMCPSearchLogsByTraceID(t *testing.T) {
	poll(t, 30*time.Second, func() error {
		payload, isErr, err := mcpTool("search_logs", `{"trace_id":"`+seededTraceID+`"}`)
		if err != nil {
			return err
		}
		if isErr {
			return fmt.Errorf("tool error: %v", payload["error"])
		}
		logs, _ := payload["logs"].([]any)
		if len(logs) != 2 {
			return fmt.Errorf("got %d logs, want the fixture's 2", len(logs))
		}
		return nil
	})
}
```

- [ ] **Step 2: Bring up the stack and run it**

Check first that no other compose project is holding these ports (`make e2e` destroys and rebuilds the shared stack):

```bash
docker compose ls
```

Then:

```bash
make e2e
```

Expected: the full `e2e` suite passes, the five `TestMCP*` cases included.

To iterate on just these against an already-running stack:

```bash
cd e2e && go test -tags e2e -run TestMCP -v ./...
```

- [ ] **Step 3: Commit**

```bash
git add e2e/mcp_test.go
git commit -m "test(e2e): the MCP server against a real stack

JSON-RPC in, the seeded fixture out: one entry span at a 100% error rate,
a three-span trace whose self time is positive and under the root's own
duration, and a misspelled service that comes back naming its neighbour."
```

---

### Task 11: Changelog, README, and closing the AEP

**Files:**
- Modify: `CHANGELOG.md` (the `## [Unreleased]` block)
- Modify: `README.md` (the modules bullet, around line 78)
- Modify: `design/2026-09-01-mcp-server.md` (file table, hop-collapse follow-up, roadmap boxes)

- [ ] **Step 1: Write the changelog entry**

In `CHANGELOG.md`, replace the line `## [Unreleased]` with:

```markdown
## [Unreleased]

### Added

- **An agent can read this estate.** This product has known a great deal about
  agents since v0.11 — the model calls your applications make, what they cost,
  when they cross a budget. Nothing let an agent know anything about it. A new
  **MCP module** serves a Model Context Protocol server at `POST /mcp`, so a
  claude.ai connector, Claude Code, or any other MCP client can investigate an
  incident against the traces, logs, error issues and health you are already
  storing — instead of a person reading a screen and retyping what they saw.

  Six read-only tools, with `service_context` as the way in: one call returns a
  service's request rate, error rate and latency percentiles, who calls it and
  what it depends on with per-path rate/error-rate/p95, its open issues, and any
  alert firing in the project. From there `search_traces`, `get_trace`,
  `search_logs` and `list_error_issues` drill down. `get_trace` carries a
  per-service **self time** — the time spent inside each service rather than
  waiting on a callee — which is the number that names the slow hop.

  Three rules do as much work as the tools. A **misspelled service name is an
  error naming the closest matches**, never an empty result: a model handed `[]`
  concludes the service is dead and reports that with confidence. A tool whose
  module is off is **absent** from the tool list rather than present and always
  failing, and a `service_context` section its install cannot answer is absent
  **and named**, because silence about a missing module reads as an absence of
  trouble. And every response is bounded and says that it is.

  Nothing new is collected, no schema changes, and no container is added — it is
  one handler on the hub, authenticated with the personal API tokens that have
  existed since v0.5, resolving its owner's live permissions. A token reads
  exactly what that person reads in the UI, in the projects they are granted.

  **The module is off by default, and here is why.** avuru-obs still makes no
  outbound call of its own. But an agent you connect to this server pulls traces
  and log bodies out of your cluster and into whichever model provider you chose,
  and log bodies are where user data lives on the installs that have any. We do
  not redact them — the line you would mask is invariably the one that explains
  the failure, which would leave the tool useless for its only job. So the switch
  is yours to throw (`modules.mcp.enabled`), the values file says this in as many
  words, and **every tool call is logged** with the token owner, the tool, its
  arguments and the row count — never the content returned. You can answer "what
  did the agent read, and whose token did it use" from the hub's own logs.

  Connect Claude Code with:

  ```bash
  claude mcp add --transport http avuruobs https://<your-hub>/mcp \
    --header "Authorization: Bearer avurut_…"
  ```

  claude.ai connectors need OAuth 2.1, which is the next step and lands
  separately. See [`design/2026-09-01-mcp-server.md`](design/2026-09-01-mcp-server.md).
```

- [ ] **Step 2: Mention it where modules are introduced**

In `README.md`, extend the modules bullet (line 78) so the list of what a module gates includes this surface. Read the bullet first, then append one sentence to it:

```markdown
  The newest is **MCP** (off by default): a Model Context Protocol server your
  agent can read the estate through — service health, dependencies, traces,
  logs and error issues — using a personal API token, with every call logged.
```

- [ ] **Step 3: Correct the AEP and tick its roadmap**

Three edits to `design/2026-09-01-mcp-server.md`.

First, the file table gained `args.go` — replace the table under "Where it lives" with:

```markdown
| File | Role |
|---|---|
| `protocol.go` | JSON-RPC 2.0 envelope, `initialize`, `tools/list`, `tools/call`, error codes |
| `tools.go` | The six tool definitions, the registry and the module gate |
| `store.go` | The narrow slice of `storage.Store` the tools read |
| `args.go` | Window translation, row bounds, service resolution and near matches |
| `context.go` | `service_context` — the composite entry point |
| `signals.go` | `list_services`, `search_traces`, `get_trace`, `search_logs`, `list_error_issues` |
| `audit.go` | The per-call structured log line |
```

Second, the sentence below that table says tools "call `storage.Store` and the API's existing DTO builders". The second half is not true and cannot be: those builders are unexported in package `api`, and an agent-facing payload is not the same shape as a wire DTO anyway. Replace that paragraph with:

```markdown
Tools call the same `storage.Store` methods the REST handlers call, and they do
not write SQL of their own — that is what keeps the two surfaces from drifting
into disagreeing about the same number, the lesson service groups taught when
the alerting evaluator turned out to be reading different config than the API
served. They build their own payload shapes rather than reusing the wire DTOs:
those are unexported in package `api`, and what an agent needs from a row is
not what a chart needs from it. The shared thing is the READ, which is the
thing that can drift.
```

Third, add the hop-collapse follow-up under the Roadmap, and tick what this work delivers:

```markdown
## Roadmap

- [ ] AEP accepted
- [x] Module registered (born OFF) + protocol skeleton behind Bearer
- [x] The six tools, bounded responses, and the audit line
- [x] e2e case + chart template test
- [ ] `service_context` recovers mesh-hidden dependencies. Today a transport
      counterpart is LABELLED (`role: "transport"`) rather than collapsed back
      into the app→app dependency the service map recovers
      ([AEP](2026-08-25-transport-hop-collapse.md)). Reimplementing that merge
      for this client would be a second set of semantics to keep in step with
      the first, so it is a follow-up rather than part of the first server.
- [ ] The Path view reads the hub's self-time instead of computing its own
- [ ] OAuth 2.1 for claude.ai connectors (own PR, own review)
- [ ] Docs: bilingual changelog, feature-status matrix, API reference, README
```

- [ ] **Step 4: Verify the whole tree**

```bash
cd hub && go test ./... && golangci-lint run && go vet ./...
cd ../ui && npx tsc --noEmit
cd ../deploy/helm && ./template-test.sh
```

Expected: all green.

- [ ] **Step 5: Prove the wedge did not move**

The claim this feature makes is that it adds no component. The check is that the time-to-value gate is unaffected:

```bash
make e2e-helm
```

Expected: `TestWedgeServiceMapUnderFiveMinutes` passes with an elapsed time in line with the previous run on `main`. If this suite is too heavy to run locally (see the colima capacity note), the render-time assertion from Task 2 — the Deployment count is unchanged with the module on — is the standing proxy, and CI runs the gate itself.

- [ ] **Step 6: Drive it with a real MCP client**

The AEP asks for one manual pass, because nothing above proves a real client can talk to this. Against the compose stack (`make up`, hub on `http://localhost:18080`), mint a token in **Settings → API tokens**, then:

```bash
claude mcp add --transport http avuruobs http://localhost:18080/mcp \
  --header "Authorization: Bearer avurut_…"
claude mcp list
```

Then open a session and run one real investigation — "what is wrong with seed-checkout?" — and check three things by eye: the tool list is the six, `service_context` comes back first, and a deliberately misspelled service produces the near matches rather than a shrug. Remove it afterwards with `claude mcp remove avuruobs`.

If `claude mcp list` reports the server as failed, the usual cause is the token: `curl -s -X POST http://localhost:18080/mcp -H "Authorization: Bearer avurut_…" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'` isolates it in one line.

- [ ] **Step 7: Commit**

```bash
git add CHANGELOG.md README.md design/2026-09-01-mcp-server.md
git commit -m "docs: changelog and AEP for the MCP server

Also corrects two things the AEP got wrong before the code existed: the
file table had no home for argument decoding, and tools cannot reuse the
API's DTO builders (unexported, and the wrong shape for an agent) — what
they share is the READ, which is the thing that can drift. Records the
hop-collapse follow-up: a transport neighbour is labelled today, not
collapsed."
```

- [ ] **Step 8: Push and open the PR**

```bash
git push -u origin feature/mcp-server
gh pr create --title "feat: an MCP server — the estate an agent can read" --body "$(cat <<'BODY'
Serves a Model Context Protocol server from the hub at `POST /mcp`, behind a
new born-OFF `mcp` module. Six read-only tools over the traces, logs, issues
and health already stored, so an agent can investigate an incident.

AEP: `design/2026-09-01-mcp-server.md`.

**No new collection, no schema, no image, no chart component.** One handler on
the hub, authenticated with the personal API tokens that have existed since
v0.5 — a token resolves its owner's live permissions, so there is no second
authorization model to keep in sync.

**What leaves, said plainly.** The product still makes no outbound call. An
operator who connects an agent is exporting traces and log bodies by their own
hand, so: the module is off by default, `values.yaml` and the changelog say
this in as many words, and every tool call is logged with the token owner, the
tool, its arguments and the row count — never the content.

Step 2 (OAuth 2.1, which is what claude.ai connectors need) is a separate PR
with its own security review. Step 1 is useful without it: Claude Code and any
header-setting client work today.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
BODY
)"
```

- [ ] **Step 9: Align the docs site**

The docs live in a separate repository (`avuru-obs-docs`, bilingual Docusaurus). Run the `docs-align` skill against it once the engine PR is open: bilingual changelog entry, feature-status matrix row for the MCP module, API reference for `POST /mcp` and the six tools, and a setup page covering `modules.mcp.enabled`, the `claude mcp add` line, and the paragraph about what leaves the install. Commits there are unsigned — that repo rejects `commit.gpgsign=true`.

---

## Notes carried from the plan of record

- Branch `feature/mcp-server`, in the worktree `/Users/egilberny/project/avuru-obs-mcp`. The main tree is on another session's branch — do not switch it.
- Commits carry **no** `Co-Authored-By` trailer (`AI_POLICY.md`).
- `cd hub && golangci-lint run` before pushing Go. Build and vet are not enough here.
- v0.12's PR stack is still unmerged. This targets v0.13 and is based on `origin/main`, so it does not stack on it.

## Deliberately out of scope

Writes of any kind (acknowledging an alert, editing a rate, changing a group); a stdio transport; MCP resources and prompts; scoped or per-tool tokens; redacting log bodies. Each is argued in the AEP's non-goals.
