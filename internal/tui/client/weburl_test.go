package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A proxy that predates these routes answers 404. Reporting that verbatim reads
// as "the URL you asked about is missing", which is the opposite of what
// happened — the request never got as far as the URL.
func TestOlderProxyIsNamedAsSuch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New(strings.TrimPrefix(srv.URL, "http://"))
	c.token = "t"

	if _, err := c.Shorten(context.Background(), "", "https://example.com/"); err == nil {
		t.Fatal("expected an error from a proxy with no /shorten")
	} else {
		if !strings.Contains(err.Error(), "does not support") ||
			!strings.Contains(err.Error(), "older than the client") {
			t.Errorf("unhelpful error for an old proxy: %v", err)
		}
		if !strings.Contains(err.Error(), "/shorten") {
			t.Errorf("the error does not name the route: %v", err)
		}
	}
}

// The ordinary path: the proxy answers, and the client reads what it said.
func TestShortenReadsTheProxyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shorten" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer t" {
			t.Errorf("missing or wrong token: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"short": "https://s.u13.net/abc"})
	}))
	defer srv.Close()

	c := New(strings.TrimPrefix(srv.URL, "http://"))
	c.token = "t"

	got, err := c.Shorten(context.Background(), "", "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://s.u13.net/abc" {
		t.Errorf("Shorten = %q", got)
	}
}
