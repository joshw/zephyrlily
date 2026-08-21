package linkpreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// oEmbed is a published API for describing a URL, which some sites answer
// properly even though their pages carry nothing a fetcher can read.
//
// Reddit and YouTube are why this exists. Reddit serves an empty JavaScript
// shell to anything it does not recognise as a preview crawler — no Open Graph
// tags, no <h1>, and a <title> of just "Reddit" — so there is nothing in the
// markup to extract at all. YouTube does publish Open Graph tags, but behind
// 691KB of inline script, which makes one preview cost about a megabyte. Both
// answer oEmbed in well under a kilobyte.
//
// The table is a hand-checked list of hosts rather than something discovered
// from the page on purpose: discovery works by reading
// <link rel="alternate" type="application/json+oembed"> out of the markup,
// which is exactly what these sites do not give us. Every entry was verified
// against a real URL. An endpoint that later stops working costs nothing —
// Fetch falls through to reading the page as usual.
var oEmbedEndpoints = map[string]string{
	"reddit.com":  "https://www.reddit.com/oembed",
	"youtube.com": "https://www.youtube.com/oembed",
	"youtu.be":    "https://www.youtube.com/oembed",
	"vimeo.com":   "https://vimeo.com/api/oembed.json",
	"flickr.com":  "https://www.flickr.com/services/oembed",
}

// maxOEmbedBody caps the JSON read from an endpoint. Real responses run to a
// few hundred bytes; this is slack for a verbose provider and a bound on a
// broken one.
const maxOEmbedBody = 64 << 10

// errNoOEmbedTitle reports a well-formed response that described nothing, which
// is a reason to go read the page rather than a reason to give up.
var errNoOEmbedTitle = errors.New("oembed response carried no title")

// oEmbedEndpoint returns the endpoint registered for host or for any parent
// domain of it, so that www.reddit.com and old.reddit.com both resolve through
// reddit.com.
func oEmbedEndpoint(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for strings.Contains(host, ".") {
		if endpoint, ok := oEmbedEndpoints[host]; ok {
			return endpoint, true
		}
		host = host[strings.IndexByte(host, '.')+1:]
	}
	return "", false
}

// fetchOEmbed asks endpoint to describe target.
func fetchOEmbed(ctx context.Context, endpoint, target string) (Preview, error) {
	q := url.Values{"url": {target}, "format": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return Preview{}, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return Preview{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Preview{}, &StatusError{Code: resp.StatusCode}
	}

	// oEmbed carries more than this — author, thumbnail, embed HTML — but a
	// one-line preview wants the title, and the provider name is the only other
	// field that maps onto anything Preview exposes.
	var body struct {
		Title        string `json:"title"`
		ProviderName string `json:"provider_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOEmbedBody)).Decode(&body); err != nil {
		return Preview{}, fmt.Errorf("decode oembed: %w", err)
	}
	if strings.TrimSpace(body.Title) == "" {
		return Preview{}, errNoOEmbedTitle
	}

	return Preview{
		URL:      target,
		Title:    strings.TrimSpace(body.Title),
		SiteName: body.ProviderName,
		Field:    FieldOEmbed,
	}, nil
}
