package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joshw/zephyrlily/internal/webstatic"
	"github.com/stretchr/testify/require"
)

// Cache behaviour for the browser client.
//
// These matter because getting them wrong is invisible in development and
// miserable in the field: embed.FS reports a zero modification time, so
// net/http sends no Last-Modified and no ETag, and a browser handed no
// validator at all may reuse a cached 20 MB wasm indefinitely. That is what
// made a rebuilt proxy require a forced reload.

func termMux(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	require.NoError(t, addWebHandler(mux))
	return mux
}

func get(t *testing.T, h http.Handler, path string, headers ...[2]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for _, kv := range headers {
		r.Header.Set(kv[0], kv[1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestTermIndexIsRevalidatedAndStamped(t *testing.T) {
	h := termMux(t)
	build := webstatic.TermBuildID()
	require.NotEqual(t, "unknown", build, "the build ID could not be computed")

	res := get(t, h, "/term/")
	require.Equal(t, http.StatusOK, res.Code)

	body := res.Body.String()
	// The placeholder must be gone: shipped literally, every asset URL would
	// carry "__ZLILY_BUILD__" and the cache would never break.
	require.NotContains(t, body, buildIDPlaceholder, "the build placeholder was not substituted")
	require.Contains(t, body, build, "index.html does not carry the build ID")

	// index.html names every other URL, so it is the one file that must never
	// be served from cache without asking.
	require.Equal(t, "no-cache", res.Header().Get("Cache-Control"))
	require.NotEmpty(t, res.Header().Get("ETag"))
}

func TestTermIndexAnswers304ToAMatchingETag(t *testing.T) {
	h := termMux(t)
	etag := get(t, h, "/term/").Header().Get("ETag")
	require.NotEmpty(t, etag)

	// Revalidation has to be cheap, or "no-cache" would mean re-sending the
	// page on every load.
	res := get(t, h, "/term/", [2]string{"If-None-Match", etag})
	require.Equal(t, http.StatusNotModified, res.Code)
	require.Empty(t, res.Body.String())
}

func TestTermVersionedAssetsAreImmutable(t *testing.T) {
	h := termMux(t)
	build := webstatic.TermBuildID()

	res := get(t, h, "/term/term.js?v="+build)
	require.Equal(t, http.StatusOK, res.Code)
	cc := res.Header().Get("Cache-Control")
	require.Contains(t, cc, "immutable", "a versioned asset should be cached hard")
	require.Contains(t, cc, "max-age=", "a versioned asset should carry a max-age")
}

// An asset requested without a version is not safe to pin: nothing would
// dislodge it when the build changes.
func TestTermUnversionedAssetsAreRevalidated(t *testing.T) {
	res := get(t, termMux(t), "/term/term.js")
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, "no-cache", res.Header().Get("Cache-Control"))
}

func TestTermWasmContentType(t *testing.T) {
	res := get(t, termMux(t), "/term/zlily.wasm?v=x")
	if res.Code == http.StatusNotFound {
		t.Skip("zlily.wasm is not built; run the GOOS=js GOARCH=wasm build")
	}
	// instantiateStreaming refuses anything else, and Go's mime table does not
	// know .wasm on every platform.
	require.Equal(t, "application/wasm", res.Header().Get("Content-Type"))
}

func TestTermBuildEndpointReportsTheBuild(t *testing.T) {
	res := get(t, termMux(t), "/term/build")
	require.Equal(t, http.StatusOK, res.Code)
	require.Equal(t, webstatic.TermBuildID(), strings.TrimSpace(res.Body.String()))
	// An open page polls this to notice it has gone stale; a cached answer
	// would defeat the entire mechanism.
	require.Equal(t, "no-store", res.Header().Get("Cache-Control"))
}

// The bare domain gives you the terminal: nobody should have to be told to add
// /term to a URL.
func TestRootRedirectsToTheBrowserTUI(t *testing.T) {
	h := termMux(t)

	res := get(t, h, "/", [2]string{"Accept", "text/html"})
	require.Equal(t, http.StatusFound, res.Code)
	require.Equal(t, "/term/", res.Header().Get("Location"))
	// Found, not Moved Permanently: browsers cache a permanent redirect
	// indefinitely, and where the root points may change again.
	require.NotEqual(t, http.StatusMovedPermanently, res.Code)
}

// With the Svelte app no longer built, nothing catches unknown paths. They must
// 404 rather than be answered with a page, or a mistyped fetch reads markup as
// data — which is how an update banner once got stuck on every load.
func TestUnknownPathsAre404(t *testing.T) {
	h := termMux(t)
	for _, p := range []string{"/build", "/assets/index.js", "/some/deep/link"} {
		res := get(t, h, p, [2]string{"Accept", "text/html"})
		require.Equal(t, http.StatusNotFound, res.Code, "%s should be a 404", p)
	}
}

// /term/ keeps working: it is the URL in the docs and in anything shared.
func TestTermPathStillServesTheClient(t *testing.T) {
	res := get(t, termMux(t), "/term/", [2]string{"Accept", "text/html"})
	require.Equal(t, http.StatusOK, res.Code)
	require.Contains(t, res.Body.String(), "ZLILY_BUILD")
}

// Browsers ask for /favicon.ico at the site root whatever the page's link tags
// say. Answering it there as well as under /term/ keeps a 404 out of the log on
// every first visit.
func TestFaviconIsServedAtBothPaths(t *testing.T) {
	h := termMux(t)
	for _, p := range []string{"/favicon.ico", "/term/favicon.ico"} {
		res := get(t, h, p, [2]string{"Accept", "image/avif,image/webp,*/*"})
		require.Equal(t, http.StatusOK, res.Code, "%s should serve the icon", p)
		body := res.Body.Bytes()
		require.NotEmpty(t, body)
		// An ICO file starts with a 0x00 0x00 0x01 0x00 header; serving HTML
		// here would be the SPA-fallback mistake in another costume.
		require.Equal(t, []byte{0x00, 0x00, 0x01, 0x00}, body[:4],
			"%s did not return an ICO file", p)
	}
}

// The page must reference the icon, or none of the above matters.
func TestPageLinksTheIcon(t *testing.T) {
	body := get(t, termMux(t), "/term/").Body.String()
	require.Contains(t, body, `rel="icon"`)
	require.Contains(t, body, "favicon.ico")
	require.Contains(t, body, "apple-touch-icon")
	require.NotContains(t, body, buildIDPlaceholder, "the icon URLs were not stamped")
}
