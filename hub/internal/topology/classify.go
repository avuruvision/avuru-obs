package topology

import (
	"path"
	"strings"
)

// Role is what a workload does on the map.
type Role string

const (
	// RoleService is an application: something whose edges are real
	// dependencies. The default for anything unrecognised — an unknown
	// workload is a service until proven otherwise, never the reverse.
	RoleService Role = "service"
	// RoleTransport is infrastructure carrying other services' traffic. Its
	// edges are hops, not dependencies.
	RoleTransport Role = "transport"
)

// Classifier answers "service or transport" for a workload name. Build one per
// request from the current Config (it is cheap: patterns are compiled by
// path.Match at match time) and share it across the services and edges of a
// single response so one map cannot disagree with itself.
type Classifier struct {
	transport []string
	apps      []string
}

// New builds a Classifier from cfg. The zero Config yields the built-ins.
func New(cfg Config) Classifier {
	return Classifier{transport: lower(cfg.TransportPatterns()), apps: lower(cfg.Applications)}
}

// Role classifies one workload name. The applications list wins over the
// transport list, so an install can rescue a real service the patterns catch.
func (c Classifier) Role(name string) Role {
	if c.IsTransport(name) {
		return RoleTransport
	}
	return RoleService
}

// IsTransport reports whether name is transport infrastructure.
func (c Classifier) IsTransport(name string) bool {
	if name == "" {
		return false
	}
	if matchAny(c.apps, name) {
		return false
	}
	return matchAny(c.transport, name)
}

// matchAny tests name against every pattern, both whole and per segment.
//
// The per-segment pass exists because the same workload arrives under two
// shapes: OTLP resource attributes give a bare service name ("istio-proxy"),
// while OBI's k8s flow labels and some mesh integrations give
// "workload.namespace" ("global-waypoint.istio-waypoint"). Matching segments
// means one pattern covers both without the operator writing "*.foo.*" globs.
func matchAny(patterns []string, name string) bool {
	if len(patterns) == 0 {
		return false
	}
	lowered := strings.ToLower(name)
	if matchOne(patterns, lowered) {
		return true
	}
	if !strings.Contains(lowered, ".") {
		return false
	}
	for _, segment := range strings.Split(lowered, ".") {
		if matchOne(patterns, segment) {
			return true
		}
	}
	return false
}

func matchOne(patterns []string, s string) bool {
	for _, p := range patterns {
		// Validate() already rejected malformed patterns, so a match error
		// here is impossible; ignoring it keeps the hot path allocation-free.
		if ok, _ := path.Match(p, s); ok {
			return true
		}
	}
	return false
}

func lower(patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]string, len(patterns))
	for i, p := range patterns {
		out[i] = strings.ToLower(strings.TrimSpace(p))
	}
	return out
}
