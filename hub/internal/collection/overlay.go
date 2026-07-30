// Package collection owns the runtime "collection overlay" — the closed,
// UI-editable subset of sensor config (design/
// 2026-07-27-collection-control-plane.md). It has no storage or Kubernetes
// dependency: it is pure data + validation, callable from both the API layer
// and (in the follow-up plan) the applier.
package collection

import (
	"encoding/json"
	"fmt"
	"strings"
)

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
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var o Overlay
	if err := dec.Decode(&o); err != nil {
		return Overlay{}, fmt.Errorf("parse overlay: %w", err)
	}
	if err := validateNamespaces(o.ExcludeNamespaces); err != nil {
		return Overlay{}, err
	}
	return o, nil
}

func validateNamespaces(ns *[]string) error {
	if ns == nil {
		return nil
	}
	for _, n := range *ns {
		if n == "" {
			return fmt.Errorf("excludeNamespaces: empty namespace name not allowed")
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
