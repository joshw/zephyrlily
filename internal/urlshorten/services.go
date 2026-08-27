package urlshorten

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// UserAgent identifies these requests, following the convention the rest of
// this client uses of saying plainly what it is rather than impersonating a
// browser.
var UserAgent = "zlily-urlshorten/1.0 (+https://github.com/joshw/zephyrlily)"

// plainService is a shortener whose entire response body is the short URL:
//
//	GET <endpoint>?url=<escaped>  ->  https://da.gd/wo4fk
//
// da.gd and tinyurl both work this way, differing only in where they live. They
// differ in how they fail — da.gd returns 400 with a plain-text reason, tinyurl
// returns the literal word "Error" — but "the body is not a URL" covers both
// without either needing its own case.
type plainService struct {
	name     string
	host     string
	endpoint string

	// expandSuffix, when set, is appended to a short URL to ask the service
	// what it points at, answered as a plain-text URL. da.gd documents "+" for
	// this; tinyurl has no equivalent and falls back to reading the redirect.
	expandSuffix string
}

func (s plainService) Name() string { return s.name }
func (s plainService) Host() string { return s.host }

func (s plainService) Shorten(ctx context.Context, rawURL string) (string, error) {
	if err := checkScheme(rawURL); err != nil {
		return "", err
	}

	q := url.Values{"url": {rawURL}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)

	body, status, err := do(req)
	if err != nil {
		return "", err
	}
	if status < 200 || status > 299 {
		return "", &StatusError{Code: status, Detail: detail(body)}
	}
	short, ok := asShortURL(body)
	if !ok {
		// A 200 that is not a URL: tinyurl's "Error", or an error page from a
		// service having a bad day. Either way there is nothing to substitute.
		return "", &ServiceError{Detail: detail(body)}
	}
	return short, nil
}

func (s plainService) Expand(ctx context.Context, shortURL string) (string, error) {
	if s.expandSuffix == "" {
		return locationOf(ctx, shortURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, shortURL+s.expandSuffix, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	// Deliberately not a browser's Accept: that is the header that makes da.gd
	// answer with its interstitial instead of an answer.
	req.Header.Set("Accept", "text/plain, */*")

	body, status, err := do(req)
	if err != nil {
		return "", err
	}
	if status < 200 || status > 299 {
		return "", &StatusError{Code: status, Detail: detail(body)}
	}
	long, ok := asAbsoluteURL(body)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNoExpansion, detail(body))
	}
	return long, nil
}

// u13Service is cj.pl's shortener: a JSON POST of {"url": …}, answered with an
// id to hang off the service's own host.
//
// The wire format is copied from CJ::shorten rather than designed here, so that
// a link shortened by this client and one shortened by the bot are the same
// link.
//
// It cannot be reached from outside its network as things stand: its nginx
// answers 403 to every POST, body or no body, http or https, while GETs of the
// same path reach the application and report "Must use POST." That is a
// method-level block rather than a dead service, which is what an IP allowlist
// on writes looks like from outside. The request below is therefore the real
// one, unstubbed — the day a credential gets past the block, this starts
// working with no other change.
type u13Service struct {
	host     string
	endpoint string
	base     string
}

func (s u13Service) Name() string { return "s.u13.net" }
func (s u13Service) Host() string { return s.host }

func (s u13Service) Shorten(ctx context.Context, rawURL string) (string, error) {
	if err := checkScheme(rawURL); err != nil {
		return "", err
	}

	body, err := json.Marshal(struct {
		URL string `json:"url"`
	}{rawURL})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if U13APIKey != "" {
		// PROVISIONAL: no credential has ever been issued for this service, so
		// the scheme below is the common convention and NOT a documented fact
		// about s.u13.net. When a real key arrives, check how it is meant to be
		// presented — a bearer token, an X- header, a query parameter, a field
		// in the JSON body — and correct this one line. Everything else on the
		// path (the setting, the environment variable, the plumbing) is
		// independent of the answer.
		req.Header.Set("Authorization", "Bearer "+U13APIKey)
	}

	respBody, status, err := do(req)
	if err != nil {
		return "", err
	}
	if status < 200 || status > 299 {
		hint := detail(respBody)
		if status == http.StatusForbidden && U13APIKey == "" {
			// The expected answer today, and an opaque one: nginx's stock 403
			// page says nothing about credentials. Name the setting rather than
			// leaving the user to guess what to do about it.
			hint = "POSTs are refused from off-network; set " + apiKeyEnv + " once a key is issued"
		}
		return "", &StatusError{Code: status, Detail: hint}
	}

	// Two spellings of "no", because the service uses both: cj.pl reads
	// "status", and the live service answers a malformed request with
	// {"message": "Must use POST."}. Either is the only explanation we will get.
	var out struct {
		ShortenedURL string `json:"shortened_url"`
		Status       string `json:"status"`
		Message      string `json:"message"`
	}
	if err := json.Unmarshal([]byte(respBody), &out); err != nil {
		return "", &ServiceError{Detail: detail(respBody)}
	}

	id := strings.TrimSpace(out.ShortenedURL)
	if id == "" {
		return "", &ServiceError{Detail: firstNonEmpty(out.Status, out.Message)}
	}

	// cj.pl treats the field as a bare id and pastes it onto the host, which is
	// what the service sends today. Handling an absolute URL too costs one
	// branch and means a service that starts returning whole links keeps
	// working; asShortURL applies the same https upgrade either way.
	if !strings.Contains(id, "://") {
		id = s.base + strings.TrimPrefix(id, "/")
	}
	short, ok := asShortURL(id)
	if !ok {
		return "", &ServiceError{Detail: detail(id)}
	}
	return short, nil
}

func (s u13Service) Expand(ctx context.Context, shortURL string) (string, error) {
	return locationOf(ctx, shortURL)
}

// locationOf asks where a short link points by reading the redirect rather than
// taking it, for services with no reverse lookup of their own.
//
// The Accept header matters as much as the no-follow does. A shortener that
// gates its interstitial on looking like a browser (da.gd) hands a plain
// request the 302 it would otherwise hide, so asking as something that is
// plainly not a browser is what gets a Location header at all.
func locationOf(ctx context.Context, shortURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, shortURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := noFollowClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))

	loc := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode > 399 || loc == "" {
		return "", fmt.Errorf("%w (http %d)", ErrNoExpansion, resp.StatusCode)
	}

	// A Location may legally be relative; resolve it against the short URL so
	// what comes back is always absolute.
	base, err := url.Parse(shortURL)
	if err != nil {
		return "", err
	}
	abs, err := base.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoExpansion, detail(loc))
	}
	long, ok := asAbsoluteURL(abs.String())
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNoExpansion, detail(loc))
	}
	return long, nil
}

// noFollowClient stops at the first response so a redirect can be read instead
// of taken.
var noFollowClient = &http.Client{
	Timeout: defaultTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// do runs one request and returns the body as text alongside the status, so
// that every caller reports a failure with what the service actually said.
func do(req *http.Request) (body string, status int, err error) {
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(b), resp.StatusCode, nil
}
