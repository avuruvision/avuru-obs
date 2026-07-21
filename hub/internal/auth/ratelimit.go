package auth

import (
	"sync"
	"time"
)

// Two independent lockout axes, both keyed within the same loginWindow:
//
//   - maxLoginAttempts (per "email|ip"): the per-account lockout. Guards
//     against an attacker guessing one account's password.
//   - maxLoginAttemptsPerIP (per ip alone): a per-IP CPU-cost cap. An
//     attacker spraying a UNIQUE, never-seen email on every request never
//     accumulates on any single "email|ip" key (each one is fresh), so
//     without this second axis they could force one bcrypt hash per request
//     forever from a single IP — the per-account limiter alone doesn't
//     bound that cost.
const (
	maxLoginAttempts      = 5
	maxLoginAttemptsPerIP = 30
	loginWindow           = time.Minute
)

// rateLimiter is a fixed-window failure counter with two independent key
// spaces (see the axes above). Memory is bounded by pruning expired windows
// on each check.
type rateLimiter struct {
	mu        sync.Mutex
	windows   map[string]*window // per "email|ip" — account lockout
	ipWindows map[string]*window // per ip — CPU-cost cap
	now       func() time.Time   // injectable clock, for deterministic tests
}

type window struct {
	start time.Time
	count int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		windows:   map[string]*window{},
		ipWindows: map[string]*window{},
		now:       time.Now,
	}
}

// blocked reports whether key (the per-account axis) or ip (the per-IP axis)
// is over its limit right now. Note: a successful login deliberately does
// NOT reset either window (standard lockout; an attacker guessing right
// while blocked learns nothing immediately).
func (l *rateLimiter) blocked(key, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	if w := l.windows[key]; w != nil && w.count >= maxLoginAttempts {
		return true
	}
	if w := l.ipWindows[ip]; w != nil && w.count >= maxLoginAttemptsPerIP {
		return true
	}
	return false
}

// fail records one failed attempt against both axes.
func (l *rateLimiter) fail(key, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	bump(l.windows, key, l.now())
	bump(l.ipWindows, ip, l.now())
}

func bump(m map[string]*window, key string, now time.Time) {
	w := m[key]
	if w == nil || now.Sub(w.start) > loginWindow {
		m[key] = &window{start: now, count: 1}
		return
	}
	w.count++
}

func (l *rateLimiter) prune() {
	now := l.now()
	pruneWindows(l.windows, now)
	pruneWindows(l.ipWindows, now)
}

func pruneWindows(m map[string]*window, now time.Time) {
	for k, w := range m {
		if now.Sub(w.start) > loginWindow {
			delete(m, k)
		}
	}
}
