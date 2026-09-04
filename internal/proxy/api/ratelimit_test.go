package api

import (
	"net/http"
	"testing"
	"time"
)

func TestAuthLimiterLocksOutAfterRepeatedFailures(t *testing.T) {
	l := newAuthLimiter(3, time.Minute, time.Minute)
	now := time.Now()

	for i := 0; i < 2; i++ {
		if ok, _ := l.allow("1.2.3.4", now); !ok {
			t.Fatalf("locked out after %d failures, allowance is 3", i)
		}
		l.recordFailure("1.2.3.4", now)
	}

	// The third failure reaches the allowance and closes the door.
	if ok, _ := l.allow("1.2.3.4", now); !ok {
		t.Fatal("locked out before using the full allowance")
	}
	l.recordFailure("1.2.3.4", now)

	ok, retry := l.allow("1.2.3.4", now)
	if ok {
		t.Fatal("still allowed after exhausting the allowance")
	}
	if retry <= 0 {
		t.Errorf("retry-after = %v, want a positive wait", retry)
	}

	// One client's failures must not affect anyone else.
	if ok, _ := l.allow("5.6.7.8", now); !ok {
		t.Error("a different client was locked out by the first one's failures")
	}
}

func TestAuthLimiterReleasesAfterLockout(t *testing.T) {
	l := newAuthLimiter(1, time.Minute, 30*time.Second)
	now := time.Now()
	l.recordFailure("1.2.3.4", now)

	if ok, _ := l.allow("1.2.3.4", now); ok {
		t.Fatal("expected a lockout")
	}
	if ok, _ := l.allow("1.2.3.4", now.Add(31*time.Second)); !ok {
		t.Fatal("still locked out after the lockout elapsed")
	}
	// Serving the lockout must restore the full allowance, not leave the
	// client one failure from being locked out again.
	if ok, _ := l.allow("1.2.3.4", now.Add(31*time.Second)); !ok {
		t.Error("allowance was not reset after the lockout was served")
	}
}

func TestAuthLimiterForgetsStaleFailures(t *testing.T) {
	l := newAuthLimiter(2, time.Minute, time.Minute)
	now := time.Now()

	l.recordFailure("1.2.3.4", now)
	// A failure long after the window opened starts a fresh window rather than
	// accumulating, so occasional typos never add up to a lockout.
	l.recordFailure("1.2.3.4", now.Add(2*time.Minute))
	if ok, _ := l.allow("1.2.3.4", now.Add(2*time.Minute)); !ok {
		t.Error("stale failures were counted toward the allowance")
	}
}

func TestAuthLimiterSuccessClearsHistory(t *testing.T) {
	l := newAuthLimiter(2, time.Minute, time.Minute)
	now := time.Now()
	l.recordFailure("1.2.3.4", now)
	l.recordSuccess("1.2.3.4")
	l.recordFailure("1.2.3.4", now)
	if ok, _ := l.allow("1.2.3.4", now); !ok {
		t.Error("a correct password did not clear the earlier failure")
	}
}

func TestAuthLimiterPrunesRecords(t *testing.T) {
	l := newAuthLimiter(5, time.Minute, time.Minute)
	now := time.Now()
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		l.recordFailure(ip, now)
	}
	// An attacker rotating addresses must not grow the map without bound.
	l.allow("4.4.4.4", now.Add(2*time.Minute))
	l.mu.Lock()
	n := len(l.clients)
	l.mu.Unlock()
	if n != 0 {
		t.Errorf("kept %d stale records, want them pruned", n)
	}
}

func TestClientIP(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote string
		xff    string
		real   string
		trust  bool
		want   string
	}{
		{"direct connection", "203.0.113.9:5555", "", "", false, "203.0.113.9"},
		{"untrusted XFF is ignored", "203.0.113.9:5555", "1.2.3.4", "", false, "203.0.113.9"},
		// Rightmost is what the nearest proxy appended; the leftmost entries
		// are client-supplied and would let an attacker mint fresh allowances.
		{"trusted XFF takes the rightmost", "10.0.0.2:5555", "1.2.3.4, 198.51.100.7", "", true, "198.51.100.7"},
		{"trusted single XFF", "10.0.0.2:5555", "198.51.100.7", "", true, "198.51.100.7"},
		{"X-Real-IP fallback", "10.0.0.2:5555", "", "198.51.100.7", true, "198.51.100.7"},
		{"trusted but no headers", "10.0.0.2:5555", "", "", true, "10.0.0.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPost, "/auth", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.real != "" {
				r.Header.Set("X-Real-IP", tc.real)
			}
			if got := clientIP(r, tc.trust); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
