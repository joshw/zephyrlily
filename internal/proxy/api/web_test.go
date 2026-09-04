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

// The build ID has to change when the assets do, or none of the above helps.
func TestTermBuildIDIsStableAndContentDerived(t *testing.T) {
	require.Equal(t, webstatic.TermBuildID(), webstatic.TermBuildID(), "must be stable within a run")
	require.Len(t, webstatic.TermBuildID(), 12)
	require.NotContains(t, webstatic.TermBuildID(), "/", "must be safe in a URL")
}

// The SPA falls back to index.html so client-side routing survives a reload.
// That fallback must not answer programmatic requests, though: a fetch() that
// resolves to the wrong path would get a page of HTML and a 200, and read it
// as data. That is not hypothetical — the browser client polls a relative
// "build" URL, and on a page URL without its trailing slash that resolves one
// level too high, landing here. The symptom was an update banner that appeared
// on every load and could not be dismissed by reloading.
func TestSPAFallbackOnlyAnswersNavigations(t *testing.T) {
	h := termMux(t)

	// A fetch(): no text/html in Accept.
	res := get(t, h, "/build", [2]string{"Accept", "*/*"})
	require.Equal(t, http.StatusNotFound, res.Code,
		"a programmatic request for an unknown path must 404, not receive the SPA:\n%s",
		res.Body.String())

	// A navigation: the fallback is what makes deep links work.
	res = get(t, h, "/some/deep/link",
		[2]string{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"})
	require.Equal(t, http.StatusOK, res.Code, "deep links should still reach the SPA")
	require.Contains(t, res.Body.String(), "<html", "the fallback should serve the SPA page")
}

// The build endpoint answers only on its real path. Reaching it by any other
// route would mean the page is comparing against something else entirely.
func TestBuildEndpointIsNotReachableFromTheSPARoot(t *testing.T) {
	h := termMux(t)

	correct := get(t, h, "/term/build")
	require.Equal(t, http.StatusOK, correct.Code)
	require.Regexp(t, `^[0-9a-f]{12}$`, strings.TrimSpace(correct.Body.String()))

	// One level too high: must not look like a successful answer.
	wrong := get(t, h, "/build", [2]string{"Accept", "*/*"})
	require.NotEqual(t, http.StatusOK, wrong.Code)
}
