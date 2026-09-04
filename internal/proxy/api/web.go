package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joshw/zephyrlily/internal/webstatic"
)

// buildTLSConfig returns a *tls.Config using the provided cert/key files, or
// a freshly generated self-signed ECDSA certificate if both paths are empty.
func (s *Server) buildTLSConfig() (*tls.Config, error) {
	var cert tls.Certificate
	var err error

	if s.cfg.WebCertFile != "" && s.cfg.WebKeyFile != "" {
		cert, err = tls.LoadX509KeyPair(s.cfg.WebCertFile, s.cfg.WebKeyFile)
	} else {
		cert, err = generateSelfSignedCert()
	}
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}

// generateSelfSignedCert creates an ephemeral ECDSA P-256 certificate valid
// for localhost and 127.0.0.1.  The certificate is not written to disk.
func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "zlily"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}

// spaHandler serves a compiled SPA: known files are served directly, and any
// path that doesn't resolve to a real file falls back to index.html so that
// client-side routing works after a browser reload.
type spaHandler struct {
	fs http.FileSystem
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	f, err := h.fs.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Fall back to index.html for SPA deep-linking — but only for a
			// request that is actually a navigation. Answering a fetch() with
			// a page of HTML and a 200 turns every mistaken URL into a silent
			// success: the caller reads markup where it expected data and has
			// no way to tell. A relative fetch that resolves one level too
			// high is exactly that mistake, and it is invisible until someone
			// wonders why a comparison never matches.
			if !acceptsHTML(r) {
				http.NotFound(w, r)
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			http.FileServer(h.fs).ServeHTTP(w, r2)
			return
		}
	} else {
		_ = f.Close()
	}
	http.FileServer(h.fs).ServeHTTP(w, r)
}

// acceptsHTML reports whether the request looks like a browser navigation
// rather than a programmatic fetch. Browsers ask for text/html when following
// a link or typing an address; fetch() and XHR default to */*.
func acceptsHTML(r *http.Request) bool {
	for _, accept := range r.Header.Values("Accept") {
		if strings.Contains(accept, "text/html") {
			return true
		}
	}
	return false
}

// addWebHandler registers the SPA handler on the mux.  API routes registered
// before this call take priority because Go's ServeMux prefers longer prefixes.
func addWebHandler(mux *http.ServeMux) error {
	distFS, err := webstatic.FS()
	if err != nil {
		return err
	}
	termFS, err := webstatic.TermFS()
	if err != nil {
		return err
	}
	// The browser build of the TUI, at its own prefix so it and the Svelte SPA
	// can be served side by side. Registered before "/" only for readability;
	// ServeMux picks the longer pattern regardless.
	mux.Handle("/term/", http.StripPrefix("/term/", termHandler{
		fs:      http.FS(termFS),
		buildID: webstatic.TermBuildID(),
	}))
	mux.Handle("/term", http.RedirectHandler("/term/", http.StatusMovedPermanently))
	mux.Handle("/", spaHandler{fs: http.FS(distFS)})
	return nil
}

// termHandler serves the browser TUI.
//
// Caching is the whole point of the extra work here. embed.FS reports a zero
// modification time, so net/http sends neither Last-Modified nor ETag, and a
// browser given no validator may reuse a cached copy of a 20 MB wasm binary
// indefinitely — which is why a rebuilt proxy used to need a forced reload.
//
// The fix is the standard one. Every asset is requested with the build ID in
// its query string, so a new build is a new URL: those responses are marked
// immutable and cached hard. Only index.html is revalidated, and it is small
// and carries an ETag, so the usual answer is a 304.
//
// Unlike spaHandler this does not fall back to index.html: a missing file here
// means a missing build artifact, and answering with HTML would turn that into
// an opaque WebAssembly parse error rather than a 404.
type termHandler struct {
	fs      http.FileSystem
	buildID string
}

func (h termHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch p := strings.TrimPrefix(r.URL.Path, "/"); p {
	case "build":
		// What an already-open page polls to notice it has gone stale.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, h.buildID)
		return

	case "", "index.html":
		h.serveIndex(w, r)
		return

	default:
		// Go's mime table has no entry for .wasm on every platform, and
		// instantiateStreaming refuses anything but application/wasm.
		if strings.HasSuffix(p, ".wasm") {
			w.Header().Set("Content-Type", "application/wasm")
		}
		// Safe to cache forever only because the page always asks for these
		// with ?v=<buildID>; a build that changes them changes the URL.
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("ETag", `"`+h.buildID+`"`)
		http.FileServer(h.fs).ServeHTTP(w, r)
	}
}

// serveIndex renders index.html with this build's ID substituted in. It is the
// one file that must never be served stale, since it names every other URL.
func (h termHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	f, err := h.fs.Open("index.html")
	if err != nil {
		http.Error(w, "browser client not built", http.StatusNotFound)
		return
	}
	defer func() { _ = f.Close() }()

	b, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "read index.html", http.StatusInternalServerError)
		return
	}
	body := strings.ReplaceAll(string(b), buildIDPlaceholder, h.buildID)

	etag := `"` + h.buildID + `"`
	w.Header().Set("ETag", etag)
	// no-cache means "revalidate every time", not "do not store": the browser
	// keeps the copy and usually gets a 304 back.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = io.WriteString(w, body)
}

// buildIDPlaceholder is what index.html carries where the build ID belongs.
const buildIDPlaceholder = "__ZLILY_BUILD__"
