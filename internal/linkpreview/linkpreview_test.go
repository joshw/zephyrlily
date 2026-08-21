package linkpreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serveFile returns a server that answers every request with a testdata file
// under the given Content-Type.
func serveFile(t *testing.T, name, ctype string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ctype)
		w.Write(body)
	})
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func fetch(t *testing.T, url string) (Preview, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Fetch(ctx, url)
}

func TestFetchHTML(t *testing.T) {
	srv := serveFile(t, "full_og.html", "text/html; charset=utf-8")

	p, err := fetch(t, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// The fixture carries the full set, so the summary is its headline; the
	// description is still parsed out and reported alongside.
	const want = "Seals colonise the harbour wall"
	if got := p.Summary(0); got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	if p.Field != FieldOGTitle {
		t.Errorf("Field = %q, want %q", p.Field, FieldOGTitle)
	}
	const wantDesc = "A colony of grey seals has taken up residence on the east harbour wall, delighting locals and complicating the ferry schedule."
	if p.Desc != wantDesc {
		t.Errorf("Desc = %q, want %q", p.Desc, wantDesc)
	}
}

// The charset must be recoverable from the document's own http-equiv when the
// response header does not declare one.
func TestFetchDecodesLatin1(t *testing.T) {
	srv := serveFile(t, "latin1.html", "text/html")

	p, err := fetch(t, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if want := "Une crêpe et un café crème."; p.Desc != want {
		t.Errorf("Desc = %q, want %q", p.Desc, want)
	}
	if want := "Café René"; p.Title != want {
		t.Errorf("Title = %q, want %q", p.Title, want)
	}
}

// A page with no metadata is a normal outcome, not an error.
func TestFetchNoMetadata(t *testing.T) {
	srv := serveFile(t, "empty.html", "text/html; charset=utf-8")

	p, err := fetch(t, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := p.Summary(80); got != "" {
		t.Errorf("Summary = %q, want empty", got)
	}
}

// Non-HTML must short-circuit before any attempt to parse markup.
func TestFetchNonHTML(t *testing.T) {
	var served bool
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.7\n"))
	})

	p, err := fetch(t, srv.URL+"/docs/report.pdf")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !served {
		t.Fatal("handler was never reached")
	}
	if p.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want application/pdf", p.ContentType)
	}
	if want := "PDF: report.pdf"; p.Summary(80) != want {
		t.Errorf("Summary = %q, want %q", p.Summary(80), want)
	}
}

// A body that never ends must not hang the fetch: the size cap stops the read
// and whatever was parsed before the cut is returned.
func TestFetchCapsBodySize(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><meta property="og:description" content="Before the flood.">`)
		// Far more than maxBody, and never closed by the handler.
		chunk := strings.Repeat("<!-- padding -->", 1024)
		for i := 0; i < 4*maxBody/len(chunk); i++ {
			fmt.Fprint(w, chunk)
		}
	})

	p, err := fetch(t, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if want := "Before the flood."; p.Desc != want {
		t.Errorf("Desc = %q, want %q", p.Desc, want)
	}
}

func TestFetchRedirects(t *testing.T) {
	t.Run("follows and reports the final URL", func(t *testing.T) {
		var srv *httptest.Server
		srv = serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/final" {
				http.Redirect(w, r, srv.URL+"/final", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>Arrived</title></head></html>`)
		})

		p, err := fetch(t, srv.URL+"/start")
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if want := srv.URL + "/final"; p.URL != want {
			t.Errorf("URL = %q, want %q", p.URL, want)
		}
		if p.Title != "Arrived" {
			t.Errorf("Title = %q, want %q", p.Title, "Arrived")
		}
	})

	t.Run("caps a redirect loop", func(t *testing.T) {
		var srv *httptest.Server
		var hops int
		srv = serve(t, func(w http.ResponseWriter, r *http.Request) {
			hops++
			http.Redirect(w, r, srv.URL+"/again", http.StatusFound)
		})

		_, err := fetch(t, srv.URL)
		if !errors.Is(err, ErrTooManyRedirects) {
			t.Fatalf("err = %v, want ErrTooManyRedirects", err)
		}
		if hops > maxRedirects+1 {
			t.Errorf("made %d requests, want at most %d", hops, maxRedirects+1)
		}
	})

	t.Run("refuses a redirect off http", func(t *testing.T) {
		srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		})

		if _, err := fetch(t, srv.URL); !errors.Is(err, ErrScheme) {
			t.Fatalf("err = %v, want ErrScheme", err)
		}
	})
}

func TestFetchStatusError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})

	_, err := fetch(t, srv.URL)
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *StatusError", err)
	}
	if se.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want %d", se.Code, http.StatusNotFound)
	}
}

func TestFetchRejectsScheme(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://example.test/x",
		"javascript:alert(1)",
		"/relative/path",
	} {
		if _, err := fetch(t, raw); !errors.Is(err, ErrScheme) {
			t.Errorf("Fetch(%q) err = %v, want ErrScheme", raw, err)
		}
	}
}

// The context deadline must govern a server that accepts and then stalls.
func TestFetchHonoursContext(t *testing.T) {
	// Released with defer, not t.Cleanup: cleanups run LIFO, so serve's
	// srv.Close would run first and block forever waiting on the stalled
	// handler. Defers run before any cleanup, which is the order we need.
	release := make(chan struct{})
	defer close(release)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Fetch(ctx, srv.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want the deadline to fire promptly", elapsed)
	}
}

func TestFetchSendsUserAgent(t *testing.T) {
	got := make(chan string, 1)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><head></head></html>")
	})

	if _, err := fetch(t, srv.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if ua := <-got; ua != UserAgent {
		t.Errorf("User-Agent = %q, want %q", ua, UserAgent)
	}
}

// A site that withholds its markup from strangers is asked a second time as a
// crawler it may recognise — but only when the first answer carried nothing,
// so sites that answer us honestly are never told we are someone else.
func TestFetchEscalatesToCrawlerUserAgent(t *testing.T) {
	// shell is what an allowlisting site returns to an unrecognised client:
	// valid markup naming only itself.
	const shell = `<html><head><title>Reddit</title></head><body>`
	const full = `<html><head><meta property="og:title" content="Jeep chick!!!"></head>`

	t.Run("bare page is retried and the better answer kept", func(t *testing.T) {
		var seen []string
		srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
			ua := r.Header.Get("User-Agent")
			seen = append(seen, ua)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if strings.Contains(ua, "Twitterbot") {
				fmt.Fprint(w, full)
				return
			}
			fmt.Fprint(w, shell)
		})

		p, err := fetch(t, srv.URL)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := p.Summary(0); got != "Jeep chick!!!" {
			t.Errorf("Summary = %q, want the markup only the crawler was given", got)
		}
		if len(seen) != 2 {
			t.Fatalf("made %d requests %v, want 2", len(seen), seen)
		}
		if seen[0] != UserAgent {
			t.Errorf("first request went out as %q, want to ask as ourselves first", seen[0])
		}
		if seen[1] != CrawlerUserAgent {
			t.Errorf("retry went out as %q, want %q", seen[1], CrawlerUserAgent)
		}
	})

	t.Run("page with tags is never retried", func(t *testing.T) {
		var n int
		srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
			n++
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, full)
		})
		if _, err := fetch(t, srv.URL); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if n != 1 {
			t.Errorf("made %d requests, want 1 — a page that answered needs no retry", n)
		}
	})

	t.Run("retry that adds nothing keeps the first answer", func(t *testing.T) {
		srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>Plain Old Page</title></head><body>`)
		})
		p, err := fetch(t, srv.URL)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := p.Summary(0); got != "Plain Old Page" {
			t.Errorf("Summary = %q, want the original answer preserved", got)
		}
	})

	t.Run("retry that errors keeps the first answer", func(t *testing.T) {
		srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.Header.Get("User-Agent"), "Twitterbot") {
				// CNN's behaviour: the crawler name is on a blocklist.
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>Still Useful</title></head><body>`)
		})
		p, err := fetch(t, srv.URL)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := p.Summary(0); got != "Still Useful" {
			t.Errorf("Summary = %q, want the first answer kept when the retry is refused", got)
		}
	})
}
