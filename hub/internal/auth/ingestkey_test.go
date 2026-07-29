package auth

import (
	"strings"
	"testing"
)

func TestNewIngestKey(t *testing.T) {
	raw, prefix, hash := NewIngestKey()
	if !strings.HasPrefix(raw, "avuruk_") {
		t.Fatalf("raw missing prefix: %s", raw)
	}
	if prefix != raw[:12] {
		t.Fatalf("prefix = %s, want %s", prefix, raw[:12])
	}
	if hash != HashIngestKey(raw) {
		t.Fatal("hash mismatch")
	}
	if len(hash) != 64 {
		t.Fatalf("hash not hex sha256: %q (len %d)", hash, len(hash))
	}
	// Distinct each call.
	raw2, _, _ := NewIngestKey()
	if raw == raw2 {
		t.Fatal("keys not random")
	}
}

func TestHashIngestKeyStable(t *testing.T) {
	// Same input → same hash (lookups are by exact hash).
	if HashIngestKey("avuruk_abc") != HashIngestKey("avuruk_abc") {
		t.Fatal("hash not deterministic")
	}
	if HashIngestKey("avuruk_abc") == HashIngestKey("avuruk_abd") {
		t.Fatal("distinct inputs collided")
	}
}
