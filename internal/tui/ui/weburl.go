package ui

import (
	"context"
	"errors"

	"github.com/joshw/zephyrlily/internal/linkpreview"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/urlshorten"
)

// Fetching a link's metadata, resolving a short URL and creating one all happen
// on the proxy, whichever client asked.
//
// The browser has no choice: in wasm net/http is the fetch API, so CORS blocks
// nearly every third-party site. The terminal client could do it itself, and
// used to, but should not. The s.u13.net credential belongs on the proxy — one
// place, held by whoever runs it — rather than in every client binary that
// might want to shorten something. A client built from source has no key at
// all, which is how this came to light: `go run ./cmd/zlily client` shortened
// locally, found nothing to authenticate with, and was refused.
//
// It also means the proxy's address is the only thing a client needs to reach
// the web, and that the sites being previewed see the proxy rather than
// everyone reading the discussion.

var errNoClient = errors.New("not connected to a proxy")

func fetchPreview(ctx context.Context, c *client.Client, target string) (linkpreview.Preview, error) {
	if c == nil {
		return linkpreview.Preview{}, errNoClient
	}
	return c.Preview(ctx, target)
}

func expandShortURL(ctx context.Context, c *client.Client, rawURL string) (string, error) {
	if c == nil {
		return "", errNoClient
	}
	return c.ExpandShortURL(ctx, rawURL)
}

// The service is named rather than called: the credential lives on the proxy,
// so only the proxy can talk to the shortener.
func shortenURL(ctx context.Context, c *client.Client, svc urlshorten.Service, rawURL string) (string, error) {
	if c == nil {
		return "", errNoClient
	}
	return c.Shorten(ctx, svc.Name(), rawURL)
}
