package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
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
			// Fall back to index.html for SPA deep-linking.
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

// addWebHandler registers the SPA handler on the mux.  API routes registered
// before this call take priority because Go's ServeMux prefers longer prefixes.
func addWebHandler(mux *http.ServeMux) error {
	distFS, err := webstatic.FS()
	if err != nil {
		return err
	}
	mux.Handle("/", spaHandler{fs: http.FS(distFS)})
	return nil
}
