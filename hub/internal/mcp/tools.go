package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/modules"
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
	all := s.tools()
	out := make([]toolDef, 0, len(all))
	for _, t := range all {
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
		// RESULT with isError, which is the only form an agent can reason
		// about.
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
