package linkpreview

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// meta holds the handful of <head> fields a preview can be built from. Keys in
// tags are lower-cased and carry whatever prefix the page used ("og:description",
// "twitter:description", "description"); resolving those into a preview is
// format.go's job.
type meta struct {
	title string
	tags  map[string]string

	// h1 is the document's first non-empty top-level heading, filled in only
	// when the head yielded nothing at all. See scanForH1.
	h1 string
}

// h1SearchBudget bounds how far past the head the h1 hunt will read. A page's
// first heading sits near the top of its body; anything further is a page that
// does not have one, and reading to maxBody in search of it would spend a
// megabyte to come back empty.
const h1SearchBudget = 64 << 10

// maxH1Len caps the text taken from a heading, so that a page with an
// unclosed <h1> cannot pull its whole body into a preview.
const maxH1Len = 1 << 10

// parseHead scans r for the metadata a preview needs. It stops as soon as the
// body begins — unless the head turned out to hold nothing at all, in which
// case it reads a little further to find the page's first heading.
//
// It has no error return on purpose. r is a size-capped body more often than not,
// and a tokenizer that runs out of input mid-document has still collected
// everything before the cut — which for any well-formed page is all of <head>.
// Malformed markup degrades the same way, to fewer tags rather than to a failure,
// and a preview built from fewer tags is exactly what the fallback chain in
// Summary is for.
func parseHead(r io.Reader) meta {
	counted := &countingReader{r: r}
	z := html.NewTokenizer(counted)

	m := scanHead(z)
	if m.needsH1() {
		// The head named nothing. A hand-written page often has no metadata at
		// all but does open with a heading, which is the best remaining guess
		// at what the page is called.
		m.h1 = scanForH1(z, counted)
	}
	return m
}

// scanHead collects the head metadata, leaving z positioned at the token that
// ended the scan.
func scanHead(z *html.Tokenizer) meta {
	m := meta{tags: make(map[string]string)}
	for {
		switch z.Next() {
		case html.ErrorToken:
			// Sticky: includes io.EOF and the truncation from maxBody.
			return m
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "head" {
				return m
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "body":
				return m
			case "title":
				// Advancing here can consume a token the outer loop would
				// otherwise see, but the only one that ends the scan is
				// </head>, which cannot directly follow an open <title>.
				if m.title == "" && z.Next() == html.TextToken {
					m.title = strings.TrimSpace(string(z.Text()))
				}
			case "meta":
				if hasAttr {
					m.addMeta(z)
					if m.complete() {
						// Everything a Preview draws from is in hand; the rest
						// of <head> cannot change the result. Worth checking
						// because news and video pages put hundreds of
						// kilobytes of inline script after their Open Graph
						// block, all of which would otherwise be transferred
						// just to reach </head>.
						return m
					}
				}
			}
		}
	}
}

// complete reports whether the tags collected so far already determine every
// field a Preview is built from, so that scanning further cannot change it.
//
// It requires the head of each fallback chain rather than merely something from
// it: og:title and og:description outrank every alternative, and the chains are
// resolved first-match, so once those two are present no later tag can displace
// them. og:site_name is in the test because Preview exposes it — stopping
// before it would leave the field populated or not depending on where a page
// happened to put its tags. A page that omits site_name simply never satisfies
// this and is scanned to </head> exactly as before.
func (m meta) complete() bool {
	return m.tags["og:title"] != "" &&
		m.tags["og:description"] != "" &&
		m.tags["og:site_name"] != ""
}

// addMeta records the current <meta> token's content under its property or name.
func (m *meta) addMeta(z *html.Tokenizer) {
	var key, content string
	var fromProperty bool
	for {
		// TagAttr lower-cases the key and unescapes the value for us, so
		// neither needs folding or html.UnescapeString afterwards.
		k, v, more := z.TagAttr()
		switch string(k) {
		case "property":
			// Open Graph's own attribute. It wins over name= when a tag
			// carries both, whichever order they appear in.
			key, fromProperty = strings.ToLower(strings.TrimSpace(string(v))), true
		case "name":
			if !fromProperty {
				key = strings.ToLower(strings.TrimSpace(string(v)))
			}
		case "content":
			content = strings.TrimSpace(string(v))
		}
		if !more {
			break
		}
	}
	if key == "" || content == "" {
		return
	}
	// First tag wins. Pages that repeat a key are usually emitting a good value
	// followed by a templated fallback, and taking the later one loses.
	if _, dup := m.tags[key]; !dup {
		m.tags[key] = content
	}
}

// needsH1 reports whether the head produced nothing Summary could draw on, so
// that a heading from the body is worth going to look for.
func (m meta) needsH1() bool {
	if m.title != "" {
		return false
	}
	for _, f := range summaryChain {
		if m.tags[string(f)] != "" {
			return false
		}
	}
	return true
}

// scanForH1 continues from where the head scan stopped and returns the text of
// the first heading that has any, giving up once it has read h1SearchBudget
// bytes past that point.
//
// "First heading with text" rather than "first heading": a masthead <h1> whose
// only content is the site's logo image is common, and skipping it costs one
// more tag to look at.
func scanForH1(z *html.Tokenizer, counted *countingReader) string {
	deadline := counted.n + h1SearchBudget
	for counted.n <= deadline {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken:
			if name, _ := z.TagName(); string(name) == "h1" {
				if text := headingText(z); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

// headingText accumulates the text inside an open <h1>, flattening any inline
// markup it contains, and stops at the closing tag or once maxH1Len is reached.
func headingText(z *html.Tokenizer) string {
	var sb strings.Builder
	for sb.Len() < maxH1Len {
		switch z.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(sb.String())
		case html.TextToken:
			sb.Write(z.Text())
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "h1" {
				return strings.TrimSpace(sb.String())
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// countingReader records how many bytes have been pulled from the source, which
// is what the h1 hunt budgets against — the tokenizer's own position is not
// visible, and bytes transferred is the cost that matters.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}
