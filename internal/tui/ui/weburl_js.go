//go:build js

package ui

import (
	"context"
	"errors"

	"github.com/joshw/zephyrlily/internal/linkpreview"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/urlshorten"
)

// In the browser these go through the proxy. Two reasons, either sufficient:
// wasm's net/http is the fetch API, so CORS blocks nearly every third-party
// site outright; and the shortener credential cannot be compiled into a .wasm
// that anyone who loads the page can download. See internal/proxy/api/weburl.go.

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
// so only the proxy can actually talk to the shortener.
func shortenURL(ctx context.Context, c *client.Client, svc urlshorten.Service, rawURL string) (string, error) {
	if c == nil {
		return "", errNoClient
	}
	return c.Shorten(ctx, svc.Name(), rawURL)
}
