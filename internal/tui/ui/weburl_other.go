//go:build !js

package ui

import (
	"context"

	"github.com/joshw/zephyrlily/internal/linkpreview"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/urlshorten"
)

// Native builds reach the web directly. Routing these through the proxy would
// work too, but it would make previews and shortening stop working against an
// older proxy, and there is nothing to gain: this process has no CORS policy
// and already holds its own credential.

func fetchPreview(ctx context.Context, _ *client.Client, target string) (linkpreview.Preview, error) {
	return linkpreview.Fetch(ctx, target)
}

func expandShortURL(ctx context.Context, _ *client.Client, rawURL string) (string, error) {
	return urlshorten.Expand(ctx, rawURL)
}

func shortenURL(ctx context.Context, _ *client.Client, svc urlshorten.Service, rawURL string) (string, error) {
	return svc.Shorten(ctx, rawURL)
}
