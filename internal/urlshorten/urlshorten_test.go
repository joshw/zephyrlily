package urlshorten

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubPlain returns a da.gd-shaped service backed by h.
func stubPlain(t *testing.T, h http.HandlerFunc) plainService {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return plainService{name: "stub", host: "stub.example", endpoint: srv.URL + "/shorten"}
}

// stubU13 returns an s.u13.net-shaped service backed by h.
func stubU13(t *testing.T, h http.HandlerFunc) u13Service {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return u13Service{host: "stub.example", endpoint: srv.URL + "/shorten_url", base: srv.URL + "/"}
}

func TestRegistry(t *testing.T) {
	t.Run("the default is registered", func(t *testing.T) {
		if got := Default().Name(); got != DefaultName {
			t.Errorf("Default() = %q, want %q", got, DefaultName)
		}
	})

	t.Run("all three services are present, default first", func(t *testing.T) {
		want := []string{"s.u13.net", "da.gd", "tinyurl"}
		got := Names()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("Names() = %v, want %v", got, want)
		}
		if got[0] != DefaultName {
			t.Errorf("%q should be listed first, got %q", DefaultName, got[0])
		}
	})

	t.Run("lookup is case-insensitive", func(t *testing.T) {
		for _, name := range []string{"da.gd", "DA.GD", "TinyURL", "s.u13.net"} {
			if _, ok := Lookup(name); !ok {
				t.Errorf("Lookup(%q) failed", name)
			}
		}
		if _, ok := Lookup("bit.ly"); ok {
			t.Error("Lookup found an unregistered service")
		}
	})

	t.Run("every service names a host", func(t *testing.T) {
		for _, s := range services {
			if s.Name() == "" || s.Host() == "" {
				t.Errorf("service %+v has an empty name or host", s)
			}
		}
	})
}

// A link from any registered service counts as short, not just the selected
// one — the line may hold a link someone else shortened elsewhere.
func TestIsShort(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"https://da.gd/wo4fk", true},
		{"https://tinyurl.com/2d7h5ru8", true},
		{"https://preview.tinyurl.com/2d7h5ru8", true}, // alternate front door
		{"http://s.u13.net/7f2", true},
		{"https://DA.GD/wo4fk", true},
		{"https://example.com/wo4fk", false},
		{"https://notda.gd.example.com/x", false},
		{"https://mydagd.com/x", false},
		{"not a url at all", false},
	} {
		if got := IsShort(tc.raw); got != tc.want {
			t.Errorf("IsShort(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestPlainServiceShortens(t *testing.T) {
	var gotMethod, gotURL, gotUA string
	svc := stubPlain(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotURL, gotUA = r.Method, r.URL.Query().Get("url"), r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, "https://da.gd/wo4fk\n") // da.gd sends a trailing newline
	})

	const long = "https://www.google.com/search?q=a+b%26c&hl=en#frag"
	got, err := svc.Shorten(context.Background(), long)
	if err != nil {
		t.Fatalf("Shorten: %v", err)
	}
	if got != "https://da.gd/wo4fk" {
		t.Errorf("short URL = %q, want the body with whitespace trimmed", got)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	// The query string and fragment must survive being passed through, or the
	// short link resolves somewhere other than where the user pointed.
	if gotURL != long {
		t.Errorf("sent url = %q, want %q", gotURL, long)
	}
	if gotUA != UserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, UserAgent)
	}
}

// A short link is published into a discussion, so it goes out over https even
// when the service offered http.
func TestShortLinksAreUpgradedToHTTPS(t *testing.T) {
	svc := stubPlain(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "http://da.gd/wo4fk")
	})
	got, err := svc.Shorten(context.Background(), "https://example.com/x")
	if err != nil {
		t.Fatalf("Shorten: %v", err)
	}
	if got != "https://da.gd/wo4fk" {
		t.Errorf("short URL = %q, want it upgraded to https", got)
	}
}

// da.gd reports failure with a status code, tinyurl with the word "Error" and a
// 200, and a service having a bad day with an HTML page. None of them may reach
// the input line.
func TestPlainServiceRejectsNonURLResponses(t *testing.T) {
	t.Run("status code with a reason", func(t *testing.T) {
		svc := stubPlain(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "Long URL must have http:// or https:// scheme.")
		})
		_, err := svc.Shorten(context.Background(), "https://example.com/x")
		var statusErr *StatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("err = %v, want a *StatusError", err)
		}
		if statusErr.Code != http.StatusBadRequest {
			t.Errorf("code = %d, want 400", statusErr.Code)
		}
		if !strings.Contains(statusErr.Error(), "must have http://") {
			t.Errorf("err = %q, should quote what the service said", statusErr)
		}
	})

	t.Run("tinyurl's Error with a 200", func(t *testing.T) {
		svc := stubPlain(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "Error")
		})
		_, err := svc.Shorten(context.Background(), "https://example.com/x")
		var svcErr *ServiceError
		if !errors.As(err, &svcErr) {
			t.Fatalf("err = %v, want a *ServiceError", err)
		}
		if svcErr.Detail != "Error" {
			t.Errorf("detail = %q, want %q", svcErr.Detail, "Error")
		}
	})

	t.Run("an HTML error page is not substituted, and is not quoted whole", func(t *testing.T) {
		svc := stubPlain(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "<!DOCTYPE html><html><head><title>Just a moment...</title>"+
				strings.Repeat("<p>cloudflare</p>", 200))
		})
		_, err := svc.Shorten(context.Background(), "https://example.com/x")
		if err == nil {
			t.Fatal("an HTML page was accepted as a short URL")
		}
		if len(err.Error()) > maxDetail+64 {
			t.Errorf("error is %d bytes; a whole error page should not reach the scrollback", len(err.Error()))
		}
		if strings.ContainsAny(err.Error(), "\n\r") {
			t.Error("error should be a single line")
		}
	})
}

func TestU13ServiceShortens(t *testing.T) {
	var gotMethod, gotType string
	var sent struct {
		URL string `json:"url"`
	}
	svc := stubU13(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotType = r.Method, r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = io.WriteString(w, `{"shortened_url": "7f2"}`)
	})

	got, err := svc.Shorten(context.Background(), "https://example.com/a/b")
	if err != nil {
		t.Fatalf("Shorten: %v", err)
	}
	// The id is pasted onto the base, and the result upgraded to https.
	if !strings.HasSuffix(got, "/7f2") || !strings.HasPrefix(got, "https://") {
		t.Errorf("short URL = %q, want an https link ending in /7f2", got)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}
	if sent.URL != "https://example.com/a/b" {
		t.Errorf("sent url = %q, want the full original", sent.URL)
	}
}

func TestU13ServiceReportsRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"status field, as cj.pl reads", `{"status": "invalid url"}`, "invalid url"},
		{"message field, as the live service sends", `{"message": "Must use POST."}`, "Must use POST."},
		{"no explanation at all", `{}`, "shortener returned no URL"},
		{"not even JSON", `<html>nope</html>`, "<html>nope</html>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := stubU13(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := svc.Shorten(context.Background(), "https://example.com/x")
			var svcErr *ServiceError
			if !errors.As(err, &svcErr) {
				t.Fatalf("err = %v, want a *ServiceError", err)
			}
			if svcErr.Error() != tc.want {
				t.Errorf("err = %q, want %q", svcErr, tc.want)
			}
		})
	}
}

// The 403 every off-network POST gets today is opaque on its own, so the error
// names the setting that will one day get past it.
func TestU13ForbiddenExplainsItself(t *testing.T) {
	svc := stubU13(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "<html><head><title>403 Forbidden</title></head></html>")
	})
	_, err := svc.Shorten(context.Background(), "https://example.com/x")
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v, want a *StatusError", err)
	}
	if !strings.Contains(err.Error(), apiKeyEnv) {
		t.Errorf("err = %q, should name %s", err, apiKeyEnv)
	}
}

// The key rides in an Authorization: Bearer header, confirmed against the live
// service.
func TestU13SendsAPIKeyWhenSet(t *testing.T) {
	var gotAuth string
	svc := stubU13(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"shortened_url": "7f2"}`)
	})

	old := U13APIKey
	U13APIKey = "secret-token"
	t.Cleanup(func() { U13APIKey = old })

	if _, err := svc.Shorten(context.Background(), "https://example.com/x"); err != nil {
		t.Fatalf("Shorten: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want the key to be sent", gotAuth)
	}
}

// The environment overrides the key a release build carries, so a user with a
// credential of their own does not have to rebuild to use it — and a build with
// no key compiled in still works for anyone who exports one.
func TestAPIKeyEnvironmentBeatsBuild(t *testing.T) {
	for _, tc := range []struct {
		name       string
		env, built string
		want       string
	}{
		{"the environment wins", "from-env", "from-build", "from-env"},
		{"the compiled-in key is the fallback", "", "from-build", "from-build"},
		{"neither is set", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveU13APIKey(tc.env, tc.built); got != tc.want {
				t.Errorf("resolveU13APIKey(%q, %q) = %q, want %q", tc.env, tc.built, got, tc.want)
			}
		})
	}
}

func TestU13OmitsAPIKeyWhenUnset(t *testing.T) {
	sawAuth := true
	svc := stubU13(t, func(w http.ResponseWriter, r *http.Request) {
		_, sawAuth = r.Header["Authorization"]
		_, _ = io.WriteString(w, `{"shortened_url": "7f2"}`)
	})

	old := U13APIKey
	U13APIKey = ""
	t.Cleanup(func() { U13APIKey = old })

	if _, err := svc.Shorten(context.Background(), "https://example.com/x"); err != nil {
		t.Fatalf("Shorten: %v", err)
	}
	if sawAuth {
		t.Error("an empty key should not be sent as an empty Authorization header")
	}
}

// Nothing should be sent anywhere for a URL no shortener could act on.
func TestServicesRefuseOtherSchemes(t *testing.T) {
	asked := false
	handler := func(w http.ResponseWriter, r *http.Request) { asked = true }
	for _, svc := range []Service{stubPlain(t, handler), stubU13(t, handler)} {
		for _, raw := range []string{"file:///etc/passwd", "mailto:someone@example.com"} {
			if _, err := svc.Shorten(context.Background(), raw); !errors.Is(err, ErrScheme) {
				t.Errorf("%s.Shorten(%q) err = %v, want ErrScheme", svc.Name(), raw, err)
			}
		}
	}
	if asked {
		t.Error("an unsupported URL was sent to a shortener")
	}
}

func TestServicesHonourContextCancellation(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	for _, svc := range []Service{stubPlain(t, handler), stubU13(t, handler)} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := svc.Shorten(ctx, "https://example.com/x"); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", svc.Name(), err)
		}
	}
}

// Whether a binary carries a credential is otherwise unanswerable without
// making a request and reading the failure — and a build that lost its key at
// release time looks exactly like one that never had one.
func TestKeySource(t *testing.T) {
	t.Setenv(apiKeyEnv, "")
	if got := KeySource(); got != "none" && got != "compiled in" {
		t.Errorf("KeySource() = %q, want none or compiled in", got)
	}

	t.Setenv(apiKeyEnv, "something")
	if got := KeySource(); got != "from "+apiKeyEnv {
		t.Errorf("with the environment set, KeySource() = %q", got)
	}

	// It must never be the credential itself.
	if got := KeySource(); strings.Contains(got, "something") {
		t.Errorf("KeySource() leaked the key: %q", got)
	}
}
