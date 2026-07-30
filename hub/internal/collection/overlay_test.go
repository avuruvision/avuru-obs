package collection

import "testing"

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
