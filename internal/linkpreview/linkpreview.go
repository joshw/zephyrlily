// Package linkpreview turns a URL into a short line of text describing what is
// on the other end — the same thing chat clients show when you paste a link.
//
// It reads only the metadata the page publishes about itself: Open Graph tags,
// Twitter card tags, <meta name="description">, and <title>. It does not extract
// article text and it does not summarize anything with a model. That keeps a
// preview free, fast enough to sit in front of a send, and free of any path by
// which page content could reach a language model and come back out as text
// posted under the user's name.
//
// The cost of that choice is quality: what comes back is whatever the site author
// wrote for search engines, which is often a tagline rather than a description,
// and plenty of pages publish nothing at all. Summary returns "" in that case
// rather than inventing a placeholder.
//
// Fetching a URL reveals to its host that someone is interested in it. Callers
// deciding whether to preview a link automatically should weigh that: a pasted
// URL may be an internal address, or may carry a tracking token that a fetch
// would burn.
package linkpreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
)

const (
	// defaultTimeout bounds a whole fetch, as a backstop under whatever
	// deadline the caller's context carries.
	defaultTimeout = 5 * time.Second

	// maxRedirects caps redirect hops. Shorteners chain two or three; more
	// than this is a loop or a tarpit.
	maxRedirects = 5

	// maxBody caps how much of the response we read, bounding a hostile
	// response that would otherwise stream forever.
	//
	// It is not a quality knob, and sizing it by what a reasonable page "should"
	// need was a mistake: at 256KB it silently truncated real sites before their
	// metadata. A CNN article puts og:title at byte 325K of a 4.3MB document,
	// and a YouTube watch page at 691K — both behind hundreds of kilobytes of
	// inline script. Both returned no preview at all.
	//
	// 1MB clears the observed cases with room to spare. The usual cost of
	// raising it is small because parseHead stops at </head>, and earlier still
	// once it has what it needs (see meta.complete), so this bound is only
	// reached by a page that never emits complete metadata at all.
	maxBody = 1 << 20
)

// UserAgent identifies these requests. It follows the convention set by other
// link-expanding bots (Slackbot-LinkExpanding, Twitterbot) of saying plainly
// what it is, rather than impersonating a browser: sites that serve Open Graph
// tags specifically to preview crawlers recognise this shape, and sites that
// would rather not be fetched can act on it. It is a var so smoke testing can
// try other values against sites that gate on the header.
var UserAgent = "zlily-linkpreview/1.0 (+https://github.com/joshw/zephyrlily)"

// CrawlerUserAgent is tried only after UserAgent has come back with a page that
// published nothing about itself.
//
// Several large sites serve their Open Graph tags only to an allowlist of
// preview crawlers they already know, and answer everything else — this client
// and a current Chrome string alike — with a JavaScript shell whose entire
// metadata is <title>Reddit</title>. Leading with a name those allowlists match
// is the only thing that gets the real markup; the rest of the string still
// says who we actually are and where to complain, and sites match on the
// leading token, so the disclosure costs nothing.
//
// It is deliberately the second attempt rather than the default, because the
// allowlists are not the only lists: CNN answers 403 to anything carrying
// "Twitterbot" while serving UserAgent normally. Asking as ourselves first
// keeps those sites working and means we only claim to be a crawler for pages
// that gave us nothing to begin with.
var CrawlerUserAgent = "Twitterbot/1.0 (zlily-linkpreview/1.0; +https://github.com/joshw/zephyrlily)"

var (
	// ErrScheme is returned for a URL this package will not dial.
	ErrScheme = errors.New("unsupported URL scheme")

	// ErrTooManyRedirects is returned when a URL exceeds maxRedirects hops.
	ErrTooManyRedirects = errors.New("too many redirects")
)

// StatusError reports a non-2xx response.
type StatusError struct{ Code int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("http %d %s", e.Code, http.StatusText(e.Code))
}

// client is shared so repeated previews reuse connections. Its Timeout is a
// backstop; per-call deadlines ride on the context.
var client = &http.Client{
	Timeout: defaultTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("%w (%d)", ErrTooManyRedirects, maxRedirects)
		}
		if !supportedScheme(req.URL) {
			return fmt.Errorf("%w %q in redirect", ErrScheme, req.URL.Scheme)
		}
		return nil
	},
}

// Fetch retrieves rawURL and returns what its metadata says about it.
//
// A page with no usable metadata is not an error — the returned Preview simply
// summarizes to "". Errors are reserved for never reaching the page at all: a
// scheme we will not dial, a transport failure, or a non-2xx response.
func Fetch(ctx context.Context, rawURL string) (Preview, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Preview{}, fmt.Errorf("parse url: %w", err)
	}
	if !supportedScheme(u) {
		return Preview{}, fmt.Errorf("%w %q", ErrScheme, u.Scheme)
	}

	// A host with a known oEmbed endpoint is asked directly. It comes first,
	// not as a fallback, because for these sites it is both the only thing that
	// works and far cheaper than the page: Reddit's markup carries nothing at
	// all, and YouTube's carries its tags behind most of a megabyte.
	if endpoint, ok := oEmbedEndpoint(u.Hostname()); ok {
		if p, err := fetchOEmbed(ctx, endpoint, u.String()); err == nil {
			return p, nil
		}
		// Retired endpoint, rate limit, or a URL this provider does not
		// describe. Read the page instead — the site may still have tags.
	}

	p, err := fetchPage(ctx, u, UserAgent)
	if err != nil || !worthRetryingAsCrawler(p) {
		return p, err
	}
	// The page named itself and nothing else, which is what an allowlisting
	// site returns to a stranger. Ask again as a crawler it may recognise, and
	// keep that answer only if it actually carried more.
	if alt, err := fetchPage(ctx, u, CrawlerUserAgent); err == nil && !worthRetryingAsCrawler(alt) {
		return alt, nil
	}
	return p, nil
}

// worthRetryingAsCrawler reports whether a page published nothing curated for
// sharing — no Open Graph or Twitter card tags at all, at most a bare <title>.
// That is the signature of markup withheld from us, and also of a plain old
// hand-written page, which costs one wasted request and is no worse off.
func worthRetryingAsCrawler(p Preview) bool {
	return p.ContentType == "" && (p.Field == FieldNone || p.Field == FieldTitle)
}

// fetchPage retrieves and parses u under the given identity.
func fetchPage(ctx context.Context, u *url.URL, userAgent string) (Preview, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Preview{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	req.Header.Set("Accept-Language", "en;q=0.9,*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return Preview{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Preview{}, &StatusError{Code: resp.StatusCode}
	}

	final := resp.Request.URL.String()
	ctype := resp.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(ctype)
	if err != nil && ctype != "" {
		// Unparseable header. Fall through and try to read it as markup;
		// a body that is not HTML just yields no tags.
		mediaType = ""
	}
	if mediaType != "" && !isHTML(mediaType) {
		return newTypePreview(final, mediaType), nil
	}

	// charset.NewReader sniffs a BOM, then the Content-Type charset, then the
	// document's own <meta charset> in the first bytes, and hands back UTF-8 —
	// which is what the tokenizer and every consumer downstream assume.
	body, err := charset.NewReader(io.LimitReader(resp.Body, maxBody), ctype)
	if err != nil {
		return Preview{}, fmt.Errorf("decode body: %w", err)
	}
	return newPreview(final, parseHead(body)), nil
}

func supportedScheme(u *url.URL) bool {
	return u.Scheme == "http" || u.Scheme == "https"
}

func isHTML(mediaType string) bool {
	switch strings.ToLower(mediaType) {
	case "text/html", "application/xhtml+xml":
		return true
	}
	return false
}
