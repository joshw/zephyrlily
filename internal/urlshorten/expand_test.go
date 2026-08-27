package urlshorten

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A short link cannot be read by fetching it: da.gd answers a browser-shaped
// Accept with a click-through page instead of the redirect. The reverse lookup
// asks the service directly, so what it serves whom stops mattering.
func TestPlainServiceExpandsViaReverseLookup(t *testing.T) {
	const long = "https://www.cnn.com/2026/08/26/world/live-news/nepal-flash-flooding-floods-intl"

	var gotPath, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAccept = r.URL.Path, r.Header.Get("Accept")
		if strings.HasSuffix(r.URL.Path, "+") {
			_, _ = io.WriteString(w, long+"\n")
			return
		}
		// What the real service does to anything browser-shaped.
		_, _ = io.WriteString(w, "<html><title>da.gd: shorten</title>"+
			"<meta name=description content='The da.gd URL shortening service'></html>")
	}))
	t.Cleanup(srv.Close)

	svc := plainService{name: "stub", host: "stub.example", endpoint: srv.URL, expandSuffix: "+"}
	got, err := svc.Expand(context.Background(), srv.URL+"/XFG5L")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if got != long {
		t.Errorf("Expand = %q, want %q", got, long)
	}
	if gotPath != "/XFG5L+" {
		t.Errorf("asked for %q, want the slug with + appended", gotPath)
	}
	if strings.Contains(gotAccept, "text/html") {
		t.Errorf("Accept = %q; a browser-shaped Accept is what triggers the interstitial", gotAccept)
	}
}

// An expanded URL keeps its scheme. Unlike a short link we mint and publish,
// this is somebody else's address and is only ever fetched — a host serving
// plain http may have nothing at all on 443.
func TestExpandDoesNotUpgradeScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "http://example.com/plain")
	}))
	t.Cleanup(srv.Close)

	svc := plainService{name: "stub", host: "stub.example", endpoint: srv.URL, expandSuffix: "+"}
	got, err := svc.Expand(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if got != "http://example.com/plain" {
		t.Errorf("Expand = %q, want the scheme left alone", got)
	}
}

// A service with no reverse lookup gets its redirect read instead of taken.
func TestExpandFallsBackToTheRedirect(t *testing.T) {
	const long = "https://example.com/destination?a=b#frag"
	var gotAccept string
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotAccept = r.Header.Get("Accept")
		http.Redirect(w, r, long, http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	svc := plainService{name: "stub", host: "stub.example", endpoint: srv.URL} // no expandSuffix
	got, err := svc.Expand(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if got != long {
		t.Errorf("Expand = %q, want %q", got, long)
	}
	if hits != 1 {
		t.Errorf("made %d requests; the redirect should be read, not followed", hits)
	}
	if strings.Contains(gotAccept, "text/html") {
		t.Errorf("Accept = %q; asking as a browser is what hides the redirect", gotAccept)
	}
}

func TestExpandResolvesRelativeLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)

	svc := plainService{name: "stub", host: "stub.example", endpoint: srv.URL}
	got, err := svc.Expand(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if got != srv.URL+"/elsewhere" {
		t.Errorf("Expand = %q, want the relative Location resolved to absolute", got)
	}
}

// Whatever a shortener says, it must not be mistaken for a destination.
func TestExpandRejectsNonAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"an interstitial instead of an answer", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "<html><title>da.gd: shorten</title></html>")
		}},
		{"an unknown slug", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "404 - route not found")
		}},
		{"a 200 with no redirect to read", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "")
		}},
		{"a javascript: destination", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "javascript:alert(1)")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.h)
			t.Cleanup(srv.Close)
			for _, svc := range []plainService{
				{name: "stub", host: "stub.example", endpoint: srv.URL, expandSuffix: "+"},
				{name: "stub", host: "stub.example", endpoint: srv.URL},
			} {
				if got, err := svc.Expand(context.Background(), srv.URL+"/x"); err == nil {
					t.Errorf("Expand accepted %q as a destination", got)
				}
			}
		})
	}
}

func TestExpandRoutesByHost(t *testing.T) {
	if _, err := Expand(context.Background(), "https://example.com/not-a-short-link"); !errors.Is(err, ErrNotShort) {
		t.Errorf("err = %v, want ErrNotShort", err)
	}
	// Every registered service must be reachable through the package function,
	// or a link from it would silently fall through to being previewed direct.
	//
	// The context is cancelled before the call, so routing is proved without
	// any of this reaching the network: a host that routes fails at the
	// request, and one that does not routes to ErrNotShort before trying.
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	for _, s := range services {
		if _, err := Expand(dead, "https://"+s.Host()+"/x"); errors.Is(err, ErrNotShort) {
			t.Errorf("Expand does not route %s to its service", s.Host())
		}
	}
}
