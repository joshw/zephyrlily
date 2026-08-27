package urlshorten

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// liveEnv gates the smoke test below. It is off by default because the test
// reaches third-party services and mints a real short link on each run, which
// is not something `go test ./...` should do on its own initiative.
const liveEnv = "ZLILY_SHORTEN_LIVE"

// TestLiveServices checks the registry against the real services:
//
//	ZLILY_SHORTEN_LIVE=1 go test ./internal/urlshorten/ -run Live -v
//
// It exists because the failure this package is built around is silent. Both
// s.u13.net and is.gd stopped issuing links without announcing anything, and
// each still answers requests — one with an nginx 403, the other with a 200
// carrying "database insert failed". The stub tests cannot see any of that;
// they only prove we handle the shapes we already know about. This is the check
// that says whether the default still works today.
//
// A failure here is a fact about the service, not necessarily a bug in the
// code. Read what it printed before changing anything.
func TestLiveServices(t *testing.T) {
	if os.Getenv(liveEnv) == "" {
		t.Skipf("set %s=1 to check the registry against the real services", liveEnv)
	}

	// A URL with a query string and a fragment: the parts a shortener is most
	// likely to mangle, and the ones a chat link most often carries.
	const long = "https://www.google.com/search?q=a+b%26c&hl=en&safe=off#frag"

	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			svc, ok := Lookup(name)
			if !ok {
				t.Fatalf("%s is listed by Names but not registered", name)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			short, err := svc.Shorten(ctx, long)
			if err != nil {
				if name == "s.u13.net" && U13APIKey == "" {
					// The expected result until a credential exists. Reported
					// rather than failed, so the run still says what happened.
					t.Skipf("s.u13.net refused as expected (%v)", err)
				}
				t.Fatalf("Shorten: %v", err)
			}

			t.Logf("%s -> %s", name, short)
			if !strings.HasPrefix(short, "https://") {
				t.Errorf("short URL %q is not https", short)
			}
			if !IsShort(short) {
				t.Errorf("IsShort(%q) is false; the service's host is not the one registered", short)
			}
			if len(short) >= len(long) {
				t.Errorf("short URL %q is not shorter than the original", short)
			}

			// Round-trip. This is what link previews depend on: a short link
			// cannot be read by fetching it (da.gd answers a browser-shaped
			// Accept with a click-through page of its own), so Expand is the
			// only thing standing between a pasted short link and a preview
			// that describes the shortener instead of the destination.
			back, err := svc.Expand(ctx, short)
			if err != nil {
				t.Fatalf("Expand(%s): %v", short, err)
			}
			if back != long {
				t.Errorf("Expand(%s) = %q, want the original %q", short, back, long)
			}
		})
	}
}
