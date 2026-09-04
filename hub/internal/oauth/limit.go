package oauth

import (
	"sync"
	"time"
)

// Registrar bounds unauthenticated client registration.
//
// Registration has to be open — a client that has never met this server has
// nothing to authenticate with — so these limits are what stop it being a free
// write endpoint for anyone who can reach the hub. They are not the security
// boundary: a registration grants NOTHING until a person consents. They exist
// so that abuse costs storage rather than access.
//
// Per-process, like the collection applier's mutex and the alerting evaluator's
// single loop. Under more than one replica the effective rate is the limit
// times the replica count, which is a bound, not a guarantee — said here rather
// than implied, and the reason unused registrations are also TTL-reaped by the
// migration.
type Registrar struct {
	mu      sync.Mutex
	perIP   map[string][]time.Time
	window  time.Duration
	max     int
	nowFunc func() time.Time
}

// NewRegistrar bounds registrations to max per window per client IP.
func NewRegistrar(max int, window time.Duration) *Registrar {
	return &Registrar{
		perIP:   map[string][]time.Time{},
		window:  window,
		max:     max,
		nowFunc: time.Now,
	}
}

// Allow records an attempt and reports whether it is within the limit.
func (r *Registrar) Allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.nowFunc()
	cut := now.Add(-r.window)

	kept := r.perIP[ip][:0]
	for _, t := range r.perIP[ip] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	// Bound the map itself: without this, one attempt from each of many
	// addresses grows it without limit even though no single caller is over.
	if len(kept) == 0 {
		delete(r.perIP, ip)
	} else {
		r.perIP[ip] = kept
	}
	if len(kept) >= r.max {
		return false
	}
	r.perIP[ip] = append(r.perIP[ip], now)
	return true
}
