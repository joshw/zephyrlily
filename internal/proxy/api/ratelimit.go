package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Brute-force protection for POST /auth.
//
// The proxy will attempt a Lily login for anyone who can reach it, which makes
// an internet-facing proxy a convenient engine for guessing passwords on the
// Lily server behind it — at whatever rate the attacker likes, from this
// host's address. This limits failed attempts per client and caps how many
// sessions (and therefore Lily connections) strangers can cause to exist.

const (
	// defaultAuthMaxFailures is how many failures one client may accumulate
	// inside defaultAuthWindow before it is made to wait. Set high enough that
	// a person fumbling their password never notices.
	defaultAuthMaxFailures = 5
	defaultAuthWindow      = 5 * time.Minute
	defaultAuthLockout     = 5 * time.Minute

	// defaultMaxSessions bounds concurrent sessions, each of which owns a TCP
	// connection to the Lily server.
	defaultMaxSessions = 64
)

// authLimiter tracks failed authentication attempts per client key.
type authLimiter struct {
	maxFailures int
	window      time.Duration
	lockout     time.Duration

	mu      sync.Mutex
	clients map[string]*authRecord
}

type authRecord struct {
	failures int
	first    time.Time // when the current window opened
	until    time.Time // locked out until this time; zero when not locked
}

func newAuthLimiter(maxFailures int, window, lockout time.Duration) *authLimiter {
	if maxFailures <= 0 {
		maxFailures = defaultAuthMaxFailures
	}
	if window <= 0 {
		window = defaultAuthWindow
	}
	if lockout <= 0 {
		lockout = defaultAuthLockout
	}
	return &authLimiter{
		maxFailures: maxFailures,
		window:      window,
		lockout:     lockout,
		clients:     make(map[string]*authRecord),
	}
}

// allow reports whether key may attempt an authentication now. When it may
// not, the second return is how long the caller should be told to wait.
func (l *authLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(now)

	rec, ok := l.clients[key]
	if !ok {
		return true, 0
	}
	if !rec.until.IsZero() {
		if now.Before(rec.until) {
			return false, rec.until.Sub(now)
		}
		// Lockout served: start over rather than leaving the client one
		// failure away from being locked out again.
		delete(l.clients, key)
	}
	return true, 0
}

// recordFailure counts a rejected attempt, locking the client out once it has
// used up its allowance inside the window.
func (l *authLimiter) recordFailure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// A first failure, or one arriving after the window has closed, opens a
	// fresh window — occasional typos must never accumulate into a lockout.
	rec, ok := l.clients[key]
	if !ok || now.Sub(rec.first) > l.window {
		rec = &authRecord{first: now}
		l.clients[key] = rec
	}
	rec.failures++
	if rec.failures >= l.maxFailures {
		rec.until = now.Add(l.lockout)
	}
}

// recordSuccess clears a client's history: a correct password is proof enough
// that this is not the attacker the counter was there for.
func (l *authLimiter) recordSuccess(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, key)
}

// pruneLocked drops records that can no longer affect a decision, so an
// attacker rotating source addresses cannot grow the map without bound.
// Called with the mutex held.
func (l *authLimiter) pruneLocked(now time.Time) {
	for k, rec := range l.clients {
		expired := rec.until.IsZero() && now.Sub(rec.first) > l.window
		served := !rec.until.IsZero() && now.After(rec.until)
		if expired || served {
			delete(l.clients, k)
		}
	}
}

// clientIP is the key rate limiting is applied under.
//
// trustProxyHeaders must only be set when something this proxy trusts sits in
// front of it (Traefik, nginx), because X-Forwarded-For is client-supplied
// otherwise and an attacker would simply vary it to get a fresh allowance.
// The rightmost entry is the one the nearest proxy appended — the address that
// actually connected to it — so that is the one to believe, not the leftmost,
// which the client can write freely.
func clientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
