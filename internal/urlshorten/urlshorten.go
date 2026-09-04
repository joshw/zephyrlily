// Package urlshorten trades a long URL for a short one.
//
// Shorteners are not durable infrastructure. The service tigerlily's cj.pl has
// used for years (reference/tigerlily/extensions/cj.pl, CJ::shorten) now
// answers 403 to POSTs from outside its own network, and is.gd — the usual
// keyless recommendation — was, when this was written, returning "database
// insert failed" for every URL it did not already hold. Neither had announced
// anything. So the service here is pluggable and chosen at runtime by name
// (%shorten), and adding another is one entry in the registry below: the next
// one to fail should cost a setting, not a patch.
//
// Shortening publishes the URL to a third party, which learns it and keeps it
// for as long as the short link is expected to work. That is inherent to what a
// shortener is, but it makes this a poor fit for anything the user would not
// have pasted into the discussion anyway — an internal address, or a URL
// carrying a one-shot token. Nothing here shortens on its own initiative: every
// call is one the user asked for by name.
package urlshorten

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// defaultTimeout bounds a whole request, as a backstop under whatever
	// deadline the caller's context carries.
	defaultTimeout = 10 * time.Second

	// maxBody caps the response read. A short URL is a few dozen bytes and the
	// JSON wrapping one is a few dozen more; anything approaching this is an
	// error page or a captive portal, and reading it whole would gain nothing.
	maxBody = 64 << 10

	// maxDetail caps how much of a response body is quoted back in an error.
	// Services report failures as anything from one word ("Error") to a full
	// HTML page, and the whole of the latter has no business in the scrollback.
	maxDetail = 120
)

// Service is one URL shortener.
//
// Implementations are values in the registry below, immutable after
// construction, and safe to use from any goroutine — the TUI hands one to a
// tea.Cmd that runs off the UI loop.
type Service interface {
	// Name identifies the service in %shorten and in error messages.
	Name() string

	// Host is where this service's short links live, so that a link already
	// shortened can be recognised as one.
	Host() string

	// Shorten submits rawURL and returns the short URL standing for it.
	Shorten(ctx context.Context, rawURL string) (string, error)

	// Expand returns the URL a short link stands for, without following it.
	//
	// This exists because a short link cannot be read by fetching it. da.gd
	// answers a browser-shaped Accept header with a click-through interstitial
	// — "This short url was created recently… Destination hostname: www.cnn.com"
	// — served as a 200, so anything that looks like a browser (a link
	// previewer very much included) gets da.gd's own page rather than the
	// destination. Asking the service directly sidesteps the question of what
	// it decides to serve whom.
	Expand(ctx context.Context, shortURL string) (string, error)
}

// DefaultName is the service used until %shorten says otherwise.
//
// s.u13.net is the default so that a link shortened here is the link cj.pl
// would have made: the same host, in among the links the discussion already
// carries. It is the one registered service that needs a credential — release
// builds carry one compiled in, and U13APIKey says where it comes from. A build
// without a key gets a 403 from it, and da.gd is then one %shorten away.
const DefaultName = "s.u13.net"

// services is the registry, in the order %shorten lists them. The default comes
// first.
var services = []Service{
	// s.u13.net is cj.pl's shortener, so that links shortened here and by the
	// bot are the same links. Its nginx answers 403 to POSTs from off-network,
	// which is what an allowlist on writes looks like from outside; U13APIKey
	// is the credential that gets past it.
	u13Service{
		host:     "s.u13.net",
		endpoint: "http://s.u13.net/shorten_url",
		base:     "https://s.u13.net/",
	},

	// da.gd is the keyless fallback, and the one to reach for when s.u13.net
	// will not answer, because it behaves like an API: the whole response body
	// is the short URL, failures come back as real HTTP status codes with a
	// plain-text reason, and query strings and fragments survive the round trip
	// byte for byte. It answers GET /shorten?url=… with the short URL as the
	// entire body. expandSuffix is da.gd's documented reverse lookup: appending
	// + to any short URL renders the long one as plain text ("/g+", or
	// /coshorten/g).
	plainService{name: "da.gd", host: "da.gd", endpoint: "https://da.gd/shorten", expandSuffix: "+"},

	// tinyurl's keyless endpoint is the same shape. It is undocumented and
	// long-deprecated in favour of an API-key service, so it is a second
	// fallback — but it has outlived several better-documented competitors, and
	// a tinyurl link is the one most likely to still resolve in ten years. The
	// tradeoff is on delivery: tinyurl is old enough to be widely blocklisted
	// by spam filters, which da.gd is not.
	plainService{name: "tinyurl", host: "tinyurl.com", endpoint: "https://tinyurl.com/api-create.php"},
}

// apiKeyEnv names the environment variable holding the s.u13.net credential.
const apiKeyEnv = "ZLILY_SHORTEN_API_KEY"

// u13APIKeyBuild is the credential compiled into a release build, written by
// the linker:
//
//	go build -ldflags "-X github.com/joshw/zephyrlily/internal/urlshorten.u13APIKeyBuild=$KEY" ./cmd/zlily
//
// The release workflow passes the ZLILY_SHORTEN_API_KEY repository secret to
// GoReleaser, which puts it here; a plain go build leaves it empty. It has no
// initializer because -X can only write a string var that has none or a
// constant one, which is also why U13APIKey below cannot be the injected var
// itself.
//
// A key baked into a published binary is not a secret from anyone who runs
// strings on it. It is here so that s.u13.net works out of the box for someone
// who installed a release, not to keep the value private.
var u13APIKeyBuild string

// U13APIKey authenticates requests to s.u13.net.
//
// The environment wins over the compiled-in key, so a user with their own
// credential — or with a build that carries none — can supply one without
// rebuilding. It is a var so that a test, or a future %shorten subcommand, can
// set it directly.
//
// It is global rather than a field on the service because it identifies the
// user, not the endpoint — there is one of these per person, not per registry
// entry.
var U13APIKey = resolveU13APIKey(os.Getenv(apiKeyEnv), u13APIKeyBuild)

// resolveU13APIKey picks between the credential in the environment and the one
// compiled in, in that order.
func resolveU13APIKey(env, built string) string { return firstNonEmpty(env, built) }

// Lookup returns the service registered under name, matched case-insensitively.
func Lookup(name string) (Service, bool) {
	for _, s := range services {
		if strings.EqualFold(s.Name(), name) {
			return s, true
		}
	}
	return nil, false
}

// Default returns the service used when none has been chosen.
func Default() Service {
	s, ok := Lookup(DefaultName)
	if !ok {
		// Unreachable: DefaultName names a registry entry, and a test guards it.
		panic("urlshorten: default service " + DefaultName + " is not registered")
	}
	return s
}

// Names lists the registered services in the order %shorten shows them.
func Names() []string {
	out := make([]string, 0, len(services))
	for _, s := range services {
		out = append(out, s.Name())
	}
	return out
}

// IsShort reports whether rawURL is already a short link.
//
// Every registered service is consulted, not just the selected one: a line may
// hold a link someone else shortened elsewhere, and re-shortening it produces a
// link that is no shorter and takes two hops to follow, whichever service is
// currently selected.
func IsShort(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	for _, s := range services {
		if sameHost(host, s.Host()) {
			return true
		}
	}
	return false
}

// sameHost reports whether host is svcHost or a subdomain of it, so that a
// service's alternate front doors (preview.tinyurl.com) are recognised while an
// unrelated host that merely ends in the same letters is not.
func sameHost(host, svcHost string) bool {
	return strings.EqualFold(host, svcHost) ||
		strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(svcHost))
}

var (
	// ErrScheme is returned for a URL this package will not submit.
	ErrScheme = errors.New("unsupported URL scheme")

	// ErrNotShort is returned by Expand for a URL that belongs to no registered
	// service, and so has nothing to expand.
	ErrNotShort = errors.New("not a known short URL")

	// ErrNoExpansion is returned when a service would not say what a short link
	// points at — an unknown slug, or a redirect that never came.
	ErrNoExpansion = errors.New("shortener did not reveal the original URL")
)

// Expand returns the URL rawURL is a short link for.
//
// It is the reverse of Shorten and is answered by whichever registered service
// owns the host, so a link shortened by someone else — or by an older session —
// can still be resolved to what it points at.
func Expand(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	host := u.Hostname()
	for _, s := range services {
		if sameHost(host, s.Host()) {
			return s.Expand(ctx, rawURL)
		}
	}
	return "", ErrNotShort
}

// StatusError reports a non-2xx response from a shortener, with whatever the
// body said about it.
type StatusError struct {
	Code   int
	Detail string
}

func (e *StatusError) Error() string {
	s := fmt.Sprintf("http %d %s", e.Code, http.StatusText(e.Code))
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// ServiceError reports that a shortener answered, and declined. Services signal
// this in the body rather than the status line, so a 200 can still carry a
// refusal — tinyurl answers "Error" that way, and s.u13.net returns a JSON
// object with no short URL in it.
type ServiceError struct{ Detail string }

func (e *ServiceError) Error() string {
	if e.Detail == "" {
		return "shortener returned no URL"
	}
	return e.Detail
}

// client is shared so repeated shortenings reuse connections. Its Timeout is a
// backstop; per-call deadlines ride on the context.
var client = &http.Client{Timeout: defaultTimeout}

// checkScheme rejects a URL no shortener could act on, before anything is sent.
func checkScheme(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w %q", ErrScheme, u.Scheme)
	}
	return nil
}

// asAbsoluteURL validates that what a service handed back is a single absolute
// http(s) URL and nothing else.
//
// This matters more than it looks. A service having a bad day answers 200 with
// an error page, and without this the input line would be silently overwritten
// with a fragment of HTML.
func asAbsoluteURL(body string) (string, bool) {
	s := strings.TrimSpace(body)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	return s, true
}

// asShortURL validates a newly minted short link and upgrades it to https.
//
// The upgrade follows cj.pl, which rewrote the scheme on the way out: this link
// is the one that gets published into a discussion, so it should be the secure
// one even when the exchange that produced it was not. s.u13.net hands back
// http links for a host that serves https perfectly well.
//
// It applies only to links we are about to publish. An expanded URL goes
// through asAbsoluteURL instead and keeps its scheme: that one is somebody
// else's address, it is only ever fetched rather than shown, and a host that
// serves plain http may have nothing at all on 443.
func asShortURL(body string) (string, bool) {
	s, ok := asAbsoluteURL(body)
	if !ok {
		return "", false
	}
	if rest, found := strings.CutPrefix(s, "http://"); found {
		return "https://" + rest, true
	}
	return s, true
}

// detail condenses a response body into something fit to show in one line of
// scrollback.
func detail(body string) string {
	s := strings.Join(strings.Fields(body), " ")
	if len(s) > maxDetail {
		s = strings.TrimRight(s[:maxDetail-1], " ") + "…"
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// KeySource says where U13APIKey came from, without revealing it.
//
// Whether a given binary carries a credential is otherwise unanswerable
// without making a request and reading the failure, and a build that quietly
// lost its key at release time looks exactly like one that never had one.
func KeySource() string {
	switch {
	case os.Getenv(apiKeyEnv) != "":
		return "from " + apiKeyEnv
	case u13APIKeyBuild != "":
		return "compiled in"
	default:
		return "none"
	}
}
