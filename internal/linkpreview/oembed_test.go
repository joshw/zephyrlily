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
		fmt.Fprint(w, `<html><head><meta property="og:title" content="From the page"></head>`)
	})

	var gotQuery string
	api := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("url")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"title":"Jeep chick!!!","provider_name":"reddit","author_name":"someone"}`)
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
			fmt.Fprint(w, `<html>not json at all`)
		},
	}, {
		name: "well formed but describes nothing",
		handler: func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"provider_name":"reddit","title":"   "}`)
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			page := serve(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<html><head><meta property="og:title" content="From the page"></head>`)
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
		fmt.Fprint(w, `{"title":"A title"}`)
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
