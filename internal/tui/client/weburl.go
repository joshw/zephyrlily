package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/joshw/zephyrlily/internal/linkpreview"
	"github.com/joshw/zephyrlily/internal/proxy/api"
)

// Outbound web requests routed through the proxy rather than made directly.
// The browser build needs this: CORS blocks a wasm page from fetching
// arbitrary third-party sites, and the shortener credential must not ship
// inside a publicly downloadable .wasm. See internal/proxy/api/weburl.go.

// Preview asks the proxy for a link's metadata.
func (c *Client) Preview(ctx context.Context, target string) (linkpreview.Preview, error) {
	var pr api.PreviewResponse
	err := c.getJSON(ctx, c.httpURL("/urlpreview?url="+url.QueryEscape(target)), &pr)
	return pr.Preview, err
}

// ExpandShortURL asks the proxy to resolve a short link to its destination.
func (c *Client) ExpandShortURL(ctx context.Context, rawURL string) (string, error) {
	var er api.URLExpandResponse
	if err := c.getJSON(ctx, c.httpURL("/urlexpand?url="+url.QueryEscape(rawURL)), &er); err != nil {
		return "", err
	}
	return er.URL, nil
}

// Shorten asks the proxy to shorten a URL. An empty service means the proxy's
// default, which is the one holding the credential.
func (c *Client) Shorten(ctx context.Context, service, rawURL string) (string, error) {
	body, err := json.Marshal(api.ShortenRequest{Service: service, URL: rawURL})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.httpURL("/shorten"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	var sr api.ShortenResponse
	if err := c.doJSON(req, &sr); err != nil {
		return "", err
	}
	return sr.Short, nil
}

// getJSON issues an authenticated GET and decodes the response body into out.
func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, out)
}

// doJSON sends req with the session token attached and decodes the response.
// The proxy answers an upstream failure with 502 and the reason as plain text,
// so that text becomes the error rather than a bare status code.
func (c *Client) doJSON(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		// The proxy predates these routes. Say that rather than "404", which
		// reads as "the URL you asked about is missing".
		return fmt.Errorf("this proxy does not support %s; it is older than the client", req.URL.Path)
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if trimmed := string(bytes.TrimSpace(msg)); trimmed != "" {
			return fmt.Errorf("%s", trimmed)
		}
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
