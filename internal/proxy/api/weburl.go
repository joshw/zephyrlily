package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/joshw/zephyrlily/internal/linkpreview"
	"github.com/joshw/zephyrlily/internal/urlshorten"
)

// Outbound web requests on behalf of a client: previewing a link, expanding a
// short one, and shortening a long one.
//
// The native TUI can and does make these itself. The browser build cannot: in
// wasm net/http is the fetch API, and fetching an arbitrary third-party site
// from a page's origin is blocked by CORS for essentially every site worth
// previewing. Doing the work here also keeps the shortener credential
// (urlshorten.U13APIKey) on the server, where it belongs — a .wasm handed to a
// browser is a public download, and anything compiled into it is readable by
// anyone who loads the page.
//
// These are authenticated like every other route, so the proxy is not an open
// relay: only a client holding a session token can drive it.

// webRequestTimeout bounds one outbound request. The client applies its own,
// shorter deadline; this is the backstop that keeps a slow remote host from
// pinning a proxy goroutine.
const webRequestTimeout = 20 * time.Second

func (s *Server) handleURLPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessionFromRequest(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	target := r.URL.Query().Get("url")
	if target == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()

	p, err := linkpreview.Fetch(ctx, target)
	if err != nil {
		// An unreachable page is a normal outcome, not a proxy fault: the TUI
		// drops previews it cannot get rather than reporting them.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, PreviewResponse{Preview: p})
}

func (s *Server) handleURLExpand(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessionFromRequest(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	raw := r.URL.Query().Get("url")
	if raw == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()

	long, err := urlshorten.Expand(ctx, raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, URLExpandResponse{URL: long})
}

func (s *Server) handleShorten(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.sessionFromRequest(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ShortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}

	svc := urlshorten.Default()
	if req.Service != "" {
		found, ok := urlshorten.Lookup(req.Service)
		if !ok {
			http.Error(w, "unknown shortening service", http.StatusBadRequest)
			return
		}
		svc = found
	}

	ctx, cancel := context.WithTimeout(r.Context(), webRequestTimeout)
	defer cancel()

	short, err := svc.Shorten(ctx, req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, ShortenResponse{Short: short})
}
