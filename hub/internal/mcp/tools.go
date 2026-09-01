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
