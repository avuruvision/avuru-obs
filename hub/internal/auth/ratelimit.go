package auth

import (
	"sync"
	"time"
)

// maxLoginAttempts failures per key per window → ErrTooManyAttempts.
const (
	maxLoginAttempts = 5
	loginWindow      = time.Minute
)

// rateLimiter is a fixed-window failure counter keyed by "email|ip". Memory
// is bounded by pruning expired windows on each check.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
	now     func() time.Time // injectable clock, for deterministic tests
}

type window struct {
	start time.Time
	count int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{windows: map[string]*window{}, now: time.Now}
}

// blocked reports whether key is over the limit right now. Note: a
// successful login deliberately does NOT reset the window (standard
// lockout; an attacker guessing right while blocked learns nothing
// immediately).
func (l *rateLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	w := l.windows[key]
	return w != nil && w.count >= maxLoginAttempts
}

// fail records one failed attempt for key.
func (l *rateLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[key]
	if w == nil || l.now().Sub(w.start) > loginWindow {
		l.windows[key] = &window{start: l.now(), count: 1}
		return
	}
	w.count++
}

func (l *rateLimiter) prune() {
	now := l.now()
	for k, w := range l.windows {
		if now.Sub(w.start) > loginWindow {
			delete(l.windows, k)
		}
	}
}
