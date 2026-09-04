package client

import "testing"

func TestParseAddr(t *testing.T) {
	for _, tc := range []struct {
		name       string
		spec       string
		wantAddr   string
		wantSecure bool
	}{
		// What the flag has always meant, and what an embedded proxy is.
		{"host and port", "localhost:7888", "localhost:7888", false},
		{"host alone", "lily.example.org", "lily.example.org", false},

		// What a proxy behind a TLS terminator needs. Port 443 is left implicit
		// so the URL's own default applies.
		{"https", "https://lily.example.org", "lily.example.org", true},
		{"https with port", "https://lily.example.org:8443", "lily.example.org:8443", true},
		{"http explicitly", "http://localhost:7888", "localhost:7888", false},

		// The proxy address turns up in WebSocket form elsewhere; guessing
		// wrong should not be punished.
		{"wss is https", "wss://lily.example.org", "lily.example.org", true},
		{"ws is http", "ws://localhost:7888", "localhost:7888", false},

		{"trailing slash is fine", "https://lily.example.org/", "lily.example.org", true},
		{"surrounding space", "  https://lily.example.org  ", "lily.example.org", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, secure, err := ParseAddr(tc.spec)
			if err != nil {
				t.Fatalf("ParseAddr(%q): %v", tc.spec, err)
			}
			if addr != tc.wantAddr || secure != tc.wantSecure {
				t.Errorf("ParseAddr(%q) = (%q, %v), want (%q, %v)",
					tc.spec, addr, secure, tc.wantAddr, tc.wantSecure)
			}
		})
	}
}

func TestParseAddrRejects(t *testing.T) {
	for _, tc := range []struct{ name, spec, want string }{
		{"empty", "", "empty"},
		{"unknown scheme", "ftp://lily.example.org", "scheme"},
		// Silently dropping a base path would send every request to the wrong
		// place and the failure would look like a server problem.
		{"base path", "https://lily.example.org/zlily", "path"},
		{"scheme with no host", "https://", "host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ParseAddr(tc.spec); err == nil {
				t.Fatalf("ParseAddr(%q) accepted it", tc.spec)
			} else if !contains(err.Error(), tc.want) {
				t.Errorf("ParseAddr(%q) error %q does not mention %q", tc.spec, err, tc.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Dial must produce a client that speaks the right scheme, since getting this
// wrong sends plain HTTP at port 443 and earns a 404 from the reverse proxy.
func TestDialUsesTheRightScheme(t *testing.T) {
	secure, err := Dial("https://lily.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if got := secure.httpURL("/info"); got != "https://lily.example.org/info" {
		t.Errorf("https URL = %q", got)
	}
	if got := secure.wsURL("/ws"); got != "wss://lily.example.org/ws" {
		t.Errorf("wss URL = %q", got)
	}

	plain, err := Dial("localhost:7888")
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.httpURL("/info"); got != "http://localhost:7888/info" {
		t.Errorf("http URL = %q", got)
	}
	if got := plain.wsURL("/ws"); got != "ws://localhost:7888/ws" {
		t.Errorf("ws URL = %q", got)
	}
}
