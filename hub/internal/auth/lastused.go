package auth

import (
	"sync"
	"time"
)

// TouchWindow bounds how often one token's LastUsedAt is written back. Writing
// it on every authenticated request would mean one ClickHouse INSERT per API
// call for precisely the traffic pattern API tokens exist to enable, so the
// column answers "is this token still in use?" and nothing finer. It is not an
// audit trail and must not be presented as one.
const TouchWindow = time.Minute

// lastUsedPruneAt is the map size that triggers a sweep. Sized so a normal
// install never sweeps at all: it only matters for a hub that has seen more
// distinct tokens than this within a single window.
const lastUsedPruneAt = 1024

// LastUsed debounces LastUsedAt writes, per token, to at most one per
// TouchWindow. Process-local: two hub replicas each write at most once a
// window, which is still two orders of magnitude below per-request and is the
// same "good enough without leader election" trade the rest of the hub makes.
type LastUsed struct {
	mu sync.Mutex
	at map[string]time.Time
}

// ShouldTouch reports whether this token's LastUsedAt is due a write, and
// records the decision. True at most once per TouchWindow per token.
func (l *LastUsed) ShouldTouch(hash string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at == nil {
		l.at = make(map[string]time.Time)
	}
	if last, ok := l.at[hash]; ok && now.Sub(last) < TouchWindow {
		return false
	}
	// Sweep before growing. An entry older than the window would answer true
	// anyway, so dropping it changes no decision — it only stops a long-lived
	// hub from accumulating one entry per token it has ever seen, including
	// every revoked one.
	if len(l.at) >= lastUsedPruneAt {
		for k, t := range l.at {
			if now.Sub(t) >= TouchWindow {
				delete(l.at, k)
			}
		}
	}
	l.at[hash] = now
	return true
}
