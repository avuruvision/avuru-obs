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
	"github.com/avuru/avuru-obs/hub/internal/topology"
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
	// Topology is the transport classifier — which workload names are mesh
	// proxies and gateways rather than applications. It comes from the API,
	// built from the SAME hot-reloaded config the service map uses, so the map
	// and this tool cannot disagree about what a proxy is. The zero value
	// classifies nothing as transport, which is what an unmeshed install and a
	// test both want.
	Topology topology.Classifier
	Version  string // hub build version, reported by initialize
	Actor    string // who the audit line names (the token owner)
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
	if firstNonSpace(body) == '[' {
		return marshalReply(response{JSONRPC: "2.0", Error: &rpcError{codeInvalidRequest,
			"JSON-RPC batching is not supported; send one request per call"}})
	}
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
