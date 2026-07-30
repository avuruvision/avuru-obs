// Package collection owns the runtime "collection overlay" — the closed,
// UI-editable subset of sensor config (design/
// 2026-07-27-collection-control-plane.md). It has no storage or Kubernetes
// dependency: it is pure data + validation, callable from both the API layer
// and (in the follow-up plan) the applier.
package collection

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// maxExcludeNamespaces bounds the exclude list. A cluster with more than this
// many namespaces to exclude wants an opt-in discovery mode, not a list.
const maxExcludeNamespaces = 256

// dns1123Label is the Kubernetes namespace-name grammar (RFC 1123 label).
// Namespace strings from this overlay are rendered into the sensor's
// collector config by the applier, so they are validated HERE, at the trust
// boundary, rather than trusted downstream.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Overlay is the closed, UI-editable subset of sensor collection config. A
// nil field means "not overridden — use the chart's base values". This is
// the JSON shape persisted in storage.CollectionOverlay.Overlay and returned
// by GET /api/v1/collection/overlay.
type Overlay struct {
	ObiEnabled          *bool     `json:"obiEnabled,omitempty"`
	LogsEnabled         *bool     `json:"logsEnabled,omitempty"`
	KubeletstatsEnabled *bool     `json:"kubeletstatsEnabled,omitempty"`
	ProfilerEnabled     *bool     `json:"profilerEnabled,omitempty"`
	GreenEnabled        *bool     `json:"greenEnabled,omitempty"`
	ExcludeNamespaces   *[]string `json:"excludeNamespaces,omitempty"`
}

// Empty reports whether every field is unset — the "reset to chart defaults" state.
func (o Overlay) Empty() bool {
	return o.ObiEnabled == nil && o.LogsEnabled == nil && o.KubeletstatsEnabled == nil &&
		o.ProfilerEnabled == nil && o.GreenEnabled == nil && o.ExcludeNamespaces == nil
}

// ParseOverlay decodes and validates the closed schema. An empty string
// decodes to the zero Overlay (Empty() == true). Unknown JSON keys are
// rejected so the API surface can never silently widen into free-form config
// (design doc, Goals: "bounded + schema-validated").
func ParseOverlay(raw string) (Overlay, error) {
	if raw == "" {
		return Overlay{}, nil
	}
	// A top-level `null` decodes into a struct as a silent no-op, so without
	// this it would read as the empty overlay and quietly reset everything to
	// chart defaults. A client serializing null state should get an error,
	// not a wipe — DELETE is the explicit reset.
	if strings.TrimSpace(raw) == "null" {
		return Overlay{}, fmt.Errorf("parse overlay: null is not a valid overlay (use DELETE to reset)")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var o Overlay
	if err := dec.Decode(&o); err != nil {
		return Overlay{}, fmt.Errorf("parse overlay: %w", err)
	}
	// Decode reads only the FIRST JSON value and would ignore anything after
	// it, so a body like `{"obiEnabled":true} {"whatever":1}` would parse
	// clean. "Closed schema" has to mean the whole input, not its prefix.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Overlay{}, fmt.Errorf("parse overlay: unexpected trailing data after the overlay object")
	}
	if err := validateNamespaces(o.ExcludeNamespaces); err != nil {
		return Overlay{}, err
	}
	return o, nil
}

// validateNamespaces enforces the Kubernetes namespace grammar on every
// entry. The applier renders these strings into the sensor's collector
// config, so anything that isn't a legal namespace name has no legitimate
// use here and must not reach that renderer — a newline or a quote in one of
// these would be config injection, not a typo.
func validateNamespaces(ns *[]string) error {
	if ns == nil {
		return nil
	}
	if len(*ns) > maxExcludeNamespaces {
		return fmt.Errorf("excludeNamespaces: %d entries exceeds the %d maximum", len(*ns), maxExcludeNamespaces)
	}
	for _, n := range *ns {
		if n == "" {
			return fmt.Errorf("excludeNamespaces: empty namespace name not allowed")
		}
		if len(n) > 63 {
			return fmt.Errorf("excludeNamespaces: %q exceeds the 63-character namespace limit", n)
		}
		if !dns1123Label.MatchString(n) {
			return fmt.Errorf("excludeNamespaces: %q is not a valid namespace name (lowercase alphanumeric and '-', must start and end alphanumeric)", n)
		}
	}
	return nil
}

// Encode serializes the overlay back to the JSON string storage persists.
func (o Overlay) Encode() (string, error) {
	b, err := json.Marshal(o)
	if err != nil {
		return "", fmt.Errorf("encode overlay: %w", err)
	}
	return string(b), nil
}
