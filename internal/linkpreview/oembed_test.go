package linkpreview

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestOEmbedEndpointLookup(t *testing.T) {
	for _, tc := range []struct {
		host string
		want string
	}{
		{"reddit.com", "https://www.reddit.com/oembed"},
		{"www.reddit.com", "https://www.reddit.com/oembed"},
		{"old.reddit.com", "https://www.reddit.com/oembed"},
		{"WWW.Reddit.COM", "https://www.reddit.com/oembed"},
		{"www.reddit.com.", "https://www.reddit.com/oembed"}, // trailing root dot
		{"youtu.be", "https://www.youtube.com/oembed"},
		{"m.youtube.com", "https://www.youtube.com/oembed"},
		{"example.com", ""},
		{"notreddit.com", ""},
		{"reddit.com.evil.test", ""}, // parent walk must not match a prefix
		{"localhost", ""},
		{"", ""},
	} {
		t.Run(tc.host, func(t *testing.T) {
			got, ok := oEmbedEndpoint(tc.host)
			if tc.want == "" {
				if ok {
					t.Errorf("oEmbedEndpoint(%q) = %q, want no match", tc.host, got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Errorf("oEmbedEndpoint(%q) = %q/%v, want %q", tc.host, got, ok, tc.want)
			}
		})
	}
}

// withEndpoint points a host at a test server for the duration of a test.
func withEndpoint(t *testing.T, host, endpoint string) {
	t.Helper()
	prev, had := oEmbedEndpoints[host]
	oEmbedEndpoints[host] = endpoint
	t.Cleanup(func() {
		if had {
			oEmbedEndpoints[host] = prev
		} else {
			delete(oEmbedEndpoints, host)
		}
	})
}

// A host with an endpoint is described by it, and the page is never fetched.
func TestFetchPrefersOEmbed(t *testing.T) {
	var pageHits int
	page := serve(t, func(w http.ResponseWriter, r *http.Request) {
		pageHits++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<html><head><meta property="og:title" content="From the page"></head>`)
	})

	var gotQuery string
	api := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("url")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"title":"Jeep chick!!!","provider_name":"reddit","author_name":"someone"}`)
	})
	withEndpoint(t, hostOf(t, page.URL), api.URL)

	p, err := fetch(t, page.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := p.Summary(0); got != "Jeep chick!!!" {
		t.Errorf("Summary = %q, want the oEmbed title", got)
	}
	if p.Field != FieldOEmbed {
		t.Errorf("Field = %q, want %q", p.Field, FieldOEmbed)
	}
	if p.SiteName != "reddit" {
		t.Errorf("SiteName = %q", p.SiteName)
	}
	if pageHits != 0 {
		t.Errorf("page was fetched %d times; oEmbed should have made that unnecessary", pageHits)
	}
	if gotQuery != page.URL {
		t.Errorf("endpoint asked about %q, want %q", gotQuery, page.URL)
	}
}

// Anything wrong with the endpoint falls through to reading the page, so a
// retired or rate-limited API degrades rather than breaking previews.
func TestFetchFallsBackWhenOEmbedFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{{
		name: "non-2xx",
		handler: func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gone", http.StatusNotFound)
		},
	}, {
		name: "unparseable json",
		handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `<html>not json at all`)
		},
	}, {
		name: "well formed but describes nothing",
		handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"provider_name":"reddit","title":"   "}`)
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			page := serve(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = fmt.Fprint(w, `<html><head><meta property="og:title" content="From the page"></head>`)
			})
			api := serve(t, tc.handler)
			withEndpoint(t, hostOf(t, page.URL), api.URL)

			p, err := fetch(t, page.URL)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if got := p.Summary(0); got != "From the page" {
				t.Errorf("Summary = %q, want the page's own title", got)
			}
			if p.Field != FieldOGTitle {
				t.Errorf("Field = %q, want the page to have been read", p.Field)
			}
		})
	}
}

// The endpoint must be told who is asking, like every other request.
func TestOEmbedSendsUserAgent(t *testing.T) {
	got := make(chan string, 1)
	api := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("User-Agent")
		_, _ = fmt.Fprint(w, `{"title":"A title"}`)
	})
	page := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	withEndpoint(t, hostOf(t, page.URL), api.URL)

	if _, err := fetch(t, page.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ua := <-got; ua != UserAgent {
		t.Errorf("User-Agent = %q, want %q", ua, UserAgent)
	}
}

// hostOf extracts the host:port of a test server URL, which is what the
// endpoint table is keyed on.
func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	host := strings.TrimPrefix(rawURL, "http://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return host
}

// An endpoint sits behind the same crawler allowlist as the markup it stands in
// for, so a 403 there is retried under the crawler identity rather than falling
// through. For an oEmbed-only site the fall-through leads nowhere: its page is
// the empty shell the endpoint exists to avoid.
func TestFetchOEmbedEscalatesWhenRefused(t *testing.T) {
	var seen []string
	var pageHits int
	page := serve(t, func(w http.ResponseWriter, r *http.Request) {
		pageHits++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<html><head><title>Reddit</title></head><body>`)
	})
	api := serve(t, func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		seen = append(seen, ua)
		if !strings.Contains(ua, "Twitterbot") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_, _ = fmt.Fprint(w, `{"title":"Jeep chick!!!","provider_name":"reddit"}`)
	})
	withEndpoint(t, hostOf(t, page.URL), api.URL)

	p, err := fetch(t, page.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := p.Summary(0); got != "Jeep chick!!!" {
		t.Errorf("Summary = %q, want the endpoint's answer", got)
	}
	if p.Field != FieldOEmbed {
		t.Errorf("Field = %q, want %q", p.Field, FieldOEmbed)
	}
	if pageHits != 0 {
		t.Errorf("fetched the page %d times, want 0 — the endpoint answered", pageHits)
	}
	if len(seen) != 2 {
		t.Fatalf("made %d endpoint requests %v, want 2", len(seen), seen)
	}
	if seen[0] != UserAgent {
		t.Errorf("first request went out as %q, want to ask as ourselves first", seen[0])
	}
	if seen[1] != CrawlerUserAgent {
		t.Errorf("retry went out as %q, want %q", seen[1], CrawlerUserAgent)
	}
}

// A 404 from the endpoint is a retired API rather than a refusal, so it still
// falls straight through to the page without a wasted second request.
func TestFetchOEmbedDoesNotEscalateOnGone(t *testing.T) {
	var apiHits int
	page := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<html><head><meta property="og:title" content="From the page"></head>`)
	})
	api := serve(t, func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		http.Error(w, "gone", http.StatusNotFound)
	})
	withEndpoint(t, hostOf(t, page.URL), api.URL)

	p, err := fetch(t, page.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := p.Summary(0); got != "From the page" {
		t.Errorf("Summary = %q, want the page's own title", got)
	}
	if apiHits != 1 {
		t.Errorf("made %d endpoint requests, want 1", apiHits)
	}
}
