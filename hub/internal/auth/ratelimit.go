package auth

import (
	"sync"
	"time"
)

// Three independent lockout axes, all counted within the same loginWindow:
//
//   - maxLoginAttempts (per "email|ip"): the tight per-account-per-source
//     lockout. Stops one client guessing one account's password.
//   - maxLoginAttemptsPerIP (per ip alone): a per-IP CPU-cost cap. An
//     attacker spraying a UNIQUE, never-seen email on every request never
//     accumulates on any single "email|ip" key (each one is fresh), so
//     without this axis they could force one bcrypt hash per request
//     forever from a single IP — the per-account limiter alone doesn't
//     bound that cost.
//   - maxAccountAttempts (per account alone): the axis that survives IP
//     rotation. The first two both have `ip` in their key, so an attacker
//     spreading guesses over N addresses got N × maxLoginAttempts tries per
//     minute against one account and never tripped either — a botnet or any
//     cloud NAT pool made the per-account lockout decorative. Counting the
//     account on its own puts a ceiling on guesses per account per window no
//     matter how many sources they come from.
//
// The third axis is a deliberate availability trade: an attacker who can
// spend maxAccountAttempts failures a minute on someone else's address keeps
// that account's LOGIN blocked. It is bounded (the window self-heals every
// minute), it never touches an established session or a successful login, and
// the alternative — unlimited distributed guessing against a single account —
// is worse. The threshold is set well above the per-IP budget so it takes
// several distinct sources failing on one account to reach it, which no
// legitimate user does.
//
// Every ip-keyed axis uses Login's ip argument, deliberately RemoteAddr and
// not a spoofable proxy header (see handleLogin) — so behind an ingress every
// client shares the proxy's IP and the per-IP cap degenerates to a single
// GLOBAL failed-login cap. Accepted: it only throttles failed attempts and
// self-heals every loginWindow. A trusted-proxy option (trust X-Forwarded-For
// from a configured ingress CIDR, restoring true per-client accounting) is
// still open — it would sharpen axes 1 and 2, but the account axis holds
// regardless of how the source is identified, which is why it does not wait
// on that work.
const (
	maxLoginAttempts      = 5
	maxLoginAttemptsPerIP = 30
	maxAccountAttempts    = 20
	loginWindow           = time.Minute
)

// rateLimiter is a fixed-window failure counter with three independent key
// spaces (see the axes above). Memory is bounded by pruning expired windows
// on each check.
type rateLimiter struct {
	mu          sync.Mutex
	windows     map[string]*window // per "email|ip" — account-per-source lockout
	ipWindows   map[string]*window // per ip — CPU-cost cap
	acctWindows map[string]*window // per account — survives IP rotation
	now         func() time.Time   // injectable clock, for deterministic tests
}

type window struct {
	start time.Time
	count int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		windows:     map[string]*window{},
		ipWindows:   map[string]*window{},
		acctWindows: map[string]*window{},
		now:         time.Now,
	}
}

// blocked reports whether key (account-per-source), ip (per-IP) or acct
// (per-account, IP-independent) is over its limit right now. Note: a
// successful login deliberately does NOT reset any window (standard lockout;
// an attacker guessing right while blocked learns nothing immediately).
func (l *rateLimiter) blocked(key, ip, acct string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	if w := l.windows[key]; w != nil && w.count >= maxLoginAttempts {
		return true
	}
	if w := l.ipWindows[ip]; w != nil && w.count >= maxLoginAttemptsPerIP {
		return true
	}
	if w := l.acctWindows[acct]; w != nil && w.count >= maxAccountAttempts {
		return true
	}
	return false
}

// fail records one failed attempt against all three axes.
func (l *rateLimiter) fail(key, ip, acct string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	bump(l.windows, key, l.now())
	bump(l.ipWindows, ip, l.now())
	bump(l.acctWindows, acct, l.now())
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
	pruneWindows(l.acctWindows, now)
}

func pruneWindows(m map[string]*window, now time.Time) {
	for k, w := range m {
		if now.Sub(w.start) > loginWindow {
			delete(m, k)
		}
	}
}
