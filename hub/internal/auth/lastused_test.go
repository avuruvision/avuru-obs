package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestLastUsedDebouncesPerToken(t *testing.T) {
	var l LastUsed
	now := time.Now()

	if !l.ShouldTouch("a", now) {
		t.Fatal("first use of a token should write LastUsedAt")
	}
	if l.ShouldTouch("a", now.Add(TouchWindow-time.Second)) {
		t.Error("a second use inside the window wrote again — that is one INSERT per API call")
	}
	if !l.ShouldTouch("a", now.Add(TouchWindow)) {
		t.Error("a use after the window should write again")
	}
	// The debounce is per token, not global: one busy token must not stop a
	// different one from ever recording that it was used at all.
	if !l.ShouldTouch("b", now) {
		t.Error("a different token was debounced by the first one's write")
	}
}

// TestLastUsedPrunes: the map must not accumulate one entry per token the hub
// has ever seen, including every revoked one, for the life of the process.
func TestLastUsedPrunes(t *testing.T) {
	var l LastUsed
	now := time.Now()
	for i := range lastUsedPruneAt {
		l.ShouldTouch(fmt.Sprintf("tok-%d", i), now)
	}
	if len(l.at) != lastUsedPruneAt {
		t.Fatalf("map holds %d entries before any sweep, want %d", len(l.at), lastUsedPruneAt)
	}
	// One more, a full window later: every existing entry is now stale and
	// answers true anyway, so the sweep drops them all and keeps only this one.
	l.ShouldTouch("fresh", now.Add(TouchWindow))
	if len(l.at) != 1 {
		t.Fatalf("map holds %d entries after the sweep, want 1", len(l.at))
	}
	// Pruning must not change any decision: the swept token is due a write.
	if !l.ShouldTouch("tok-0", now.Add(TouchWindow)) {
		t.Error("a pruned entry changed a decision — pruning must be lossless")
	}
}
