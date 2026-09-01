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
