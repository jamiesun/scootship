// Package loginguard throttles brute-force dashboard logins.
//
// It tracks failed attempts per key (the client source IP) in a sliding window
// and locks the key out once a threshold is crossed. A successful login clears
// the key. The state is in-memory and process-local, which matches scootship's
// single-binary posture; a horizontally-scaled deployment behind a load
// balancer would need a shared backend, but a single center instance does not.
//
// The guard keys on source IP rather than username on purpose: the dashboard
// username is effectively fixed ("admin"), so a username-keyed lockout would
// both be trivially predictable and let an attacker lock out the real operator
// (a denial of service). Per-IP lockout avoids that while still stopping a
// password-guessing client.
package loginguard

import (
	"sync"
	"time"
)

// Options configures a Guard. Zero values fall back to safe defaults.
type Options struct {
	MaxFails int           // failures within Window before lockout (default 5)
	Window   time.Duration // sliding failure window (default 15m)
	Lockout  time.Duration // lockout duration once tripped (default 15m)
	MaxKeys  int           // cap on tracked keys, bounding memory (default 10000)
}

func (o *Options) withDefaults() {
	if o.MaxFails <= 0 {
		o.MaxFails = 5
	}
	if o.Window <= 0 {
		o.Window = 15 * time.Minute
	}
	if o.Lockout <= 0 {
		o.Lockout = 15 * time.Minute
	}
	if o.MaxKeys <= 0 {
		o.MaxKeys = 10000
	}
}

// Decision is the outcome of a check or a recorded failure.
type Decision struct {
	Allowed    bool          // may the key attempt a login now?
	RetryAfter time.Duration // when Allowed is false, how long until it may retry
	Remaining  int           // attempts left before lockout (informational)
}

type entry struct {
	fails       int
	windowStart time.Time
	last        time.Time
	lockedUntil time.Time
}

// Guard is a concurrency-safe per-key login throttle.
type Guard struct {
	mu   sync.Mutex
	keys map[string]*entry
	opt  Options
}

// New returns a Guard with the given options (defaults applied).
func New(opt Options) *Guard {
	opt.withDefaults()
	return &Guard{keys: make(map[string]*entry), opt: opt}
}

// Check reports whether key may attempt a login at time now. It does not record
// an attempt; call it before validating credentials.
func (g *Guard) Check(key string, now time.Time) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.keys[key]
	if e == nil {
		return Decision{Allowed: true, Remaining: g.opt.MaxFails}
	}
	if now.Before(e.lockedUntil) {
		return Decision{Allowed: false, RetryAfter: e.lockedUntil.Sub(now)}
	}
	remaining := g.opt.MaxFails - e.fails
	if remaining < 0 {
		remaining = 0
	}
	return Decision{Allowed: true, Remaining: remaining}
}

// Fail records a failed attempt for key at time now and returns the resulting
// decision. When the failure trips the threshold, Allowed is false and
// RetryAfter is the lockout duration.
func (g *Guard) Fail(key string, now time.Time) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepIfNeededLocked(now)

	e := g.keys[key]
	if e == nil {
		e = &entry{windowStart: now}
		g.keys[key] = e
	}

	switch {
	case !e.lockedUntil.IsZero() && !now.Before(e.lockedUntil):
		// A previous lockout has expired: start a fresh window.
		e.fails = 0
		e.windowStart = now
		e.lockedUntil = time.Time{}
	case now.Sub(e.windowStart) > g.opt.Window:
		// The failure window has elapsed without lockout: decay to zero.
		e.fails = 0
		e.windowStart = now
	}

	e.fails++
	e.last = now

	if e.fails >= g.opt.MaxFails {
		e.lockedUntil = now.Add(g.opt.Lockout)
		return Decision{Allowed: false, RetryAfter: g.opt.Lockout}
	}
	return Decision{Allowed: true, Remaining: g.opt.MaxFails - e.fails}
}

// Reset clears a key, e.g. after a successful login.
func (g *Guard) Reset(key string) {
	g.mu.Lock()
	delete(g.keys, key)
	g.mu.Unlock()
}

// sweepIfNeededLocked drops fully-stale entries when the map grows past the cap,
// bounding memory under a distributed guessing attack from many source IPs.
func (g *Guard) sweepIfNeededLocked(now time.Time) {
	if len(g.keys) < g.opt.MaxKeys {
		return
	}
	for k, e := range g.keys {
		if now.After(e.lockedUntil) && now.Sub(e.last) > g.opt.Window {
			delete(g.keys, k)
		}
	}
}
