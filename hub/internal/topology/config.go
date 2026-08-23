// Package topology classifies the workloads that appear on the service map.
//
// The map draws two kinds of thing that look identical in the data and are not
// the same thing at all. An APPLICATION is a service that originates or answers
// requests — a real dependency. TRANSPORT is infrastructure that carries other
// services' traffic: mesh sidecars, waypoint and ztunnel proxies, ingress and
// egress gateways. Transport shows up in traces (proxies emit spans) and in
// kernel flows (proxies exchange bytes) exactly as if it were a peer, so
// `app → proxy → app` renders as two application dependencies and the map
// claims a relationship that does not exist.
//
// This package owns the "which is it" decision in ONE place, matched by name
// and overridable per install, because the answer is install-specific: proxy
// naming differs by mesh, by version, and by whatever the operator called their
// gateway. It is pure logic — no SQL, no HTTP.
package topology

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Config is the transport-classification configuration, loaded from the mounted
// ConfigMap (AVURUOBS_TOPOLOGY_CONFIG) or Default() when absent.
type Config struct {
	// Transport adds glob patterns to the built-in list. Use it for a mesh or
	// a gateway the built-ins don't know about.
	Transport []string `json:"transport,omitempty"`
	// Applications forces RoleService for anything it matches, and wins over
	// every transport pattern. This is the escape hatch for a real service
	// whose name collides with a built-in — an application legitimately called
	// "waypoint", say.
	Applications []string `json:"applications,omitempty"`
	// DisableDefaults drops the built-in list entirely, leaving only Transport.
	// For an install where the built-ins guess wrong often enough that starting
	// from nothing is easier than subtracting.
	DisableDefaults bool `json:"disableDefaults,omitempty"`
}

// builtinTransport are the workload names that mean "this carries traffic, it
// does not originate it".
//
// Deliberately NARROW. A false positive erases a real service from the map,
// which is a worse failure than the edge noise this removes — so nothing
// generic lives here. No "*-gateway", no "*-proxy", no "istio-*": plenty of
// applications are called those, and a whole namespace matching by prefix is
// how a fix tuned on one cluster breaks every other one. Every entry below is
// either an exact upstream component name or a prefix distinctive enough that
// an application would have to be trying to collide with it.
//
// Patterns are matched against the full workload name AND against each
// dot-separated segment, so a "workload.namespace" name (what OTel resource
// attributes and OBI flow labels produce) matches on either half.
var builtinTransport = []string{
	// Istio — sidecar, ambient data plane, gateways, control plane.
	"istio-proxy",
	"istio-ingressgateway*",
	"istio-egressgateway*",
	"istiod",
	"istio-system",
	"ztunnel",
	"ztunnel-*",
	"waypoint",
	"*-waypoint",
	"waypoint-*",
	// Linkerd — the prefix is the product name, so it is safe to widen.
	"linkerd",
	"linkerd-*",
	// Consul service mesh.
	"consul-dataplane*",
	"consul-connect-envoy*",
	// Kuma / Kong Mesh.
	"kuma-dp",
	"kuma-sidecar",
	"kuma-system",
	"kuma-control-plane",
	// Envoy Gateway's managed proxies, which carry the project's own prefix.
	"envoy-gateway",
	"envoy-gateway-system",
}

// Default is the zero-config configuration: the built-in list, no additions,
// no overrides. Mirrors health.Default() — a working, sensible view with no
// config file.
func Default() Config { return Config{} }

// ParseConfig unmarshals the JSON config and validates it fail-loud (a bad
// config crashes the hub at startup rather than silently misclassifying half
// the map). Empty input yields Default().
func ParseConfig(data []byte) (Config, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return Default(), nil
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parsing topology config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate rejects empty and malformed patterns. An unparseable glob would
// otherwise match nothing forever, silently — the operator would see the map
// unchanged and have no way to tell why.
func (c Config) Validate() error {
	for _, set := range []struct {
		field    string
		patterns []string
	}{{"transport", c.Transport}, {"applications", c.Applications}} {
		for i, p := range set.patterns {
			if strings.TrimSpace(p) == "" {
				return fmt.Errorf("%s[%d] is empty", set.field, i)
			}
			if _, err := path.Match(p, "probe"); err != nil {
				return fmt.Errorf("%s[%d] %q is not a valid pattern: %w", set.field, i, p, err)
			}
		}
	}
	if c.DisableDefaults && len(c.Transport) == 0 {
		return fmt.Errorf("disableDefaults drops the built-in patterns but transport is empty — nothing would ever be classified as transport; omit disableDefaults instead")
	}
	return nil
}

// TransportPatterns is the effective transport list: the built-ins (unless
// disabled) plus the configured additions.
func (c Config) TransportPatterns() []string {
	if c.DisableDefaults {
		return c.Transport
	}
	out := make([]string, 0, len(builtinTransport)+len(c.Transport))
	out = append(out, builtinTransport...)
	return append(out, c.Transport...)
}
