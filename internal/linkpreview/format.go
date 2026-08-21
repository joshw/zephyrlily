package linkpreview

import (
	"net/url"
	"path"
	"strings"
	"unicode"
)

// Field names the metadata a preview's text was drawn from. It exists so the
// caller — today the CLI, tomorrow whatever decides whether a preview is worth
// showing — can tell an author-written description apart from a bare <title>
// scraped as a last resort.
type Field string

// Listed in the order Summary prefers them, which is the order of summaryChain.
const (
	FieldNone            Field = ""
	FieldOEmbed          Field = "oembed"
	FieldOGTitle         Field = "og:title"
	FieldTwitterTitle    Field = "twitter:title"
	FieldOGDescription   Field = "og:description"
	FieldTwitterDesc     Field = "twitter:description"
	FieldMetaDescription Field = "description"
	FieldTitle           Field = "<title>"
	FieldH1              Field = "<h1>"
	FieldContentType     Field = "content-type"
)

// Preview is what a URL resolved to. Every field may be empty: a page that
// carries no metadata at all is a normal outcome, not an error.
type Preview struct {
	// URL is the final URL, after any redirects.
	URL string

	// Title and Desc are the best available of their kind, already resolved
	// through the fallback chain.
	Title string
	Desc  string

	// SiteName is og:site_name, when the page offers one.
	SiteName string

	// ContentType is set only for non-HTML resources, where there was no
	// markup to read and the type is the only thing worth reporting.
	ContentType string

	// Field records where Summary's text comes from.
	Field Field
}

// summaryChain is the order Summary draws its text from.
//
// Headline first. A preview answers "what is on the other end of this link",
// and og:title is written to be exactly that — the page's own headline, with
// the site furniture already stripped. og:description is written for search
// snippets: it is typically a full paragraph, and on a news article it is the
// opening sentence rather than the point, so it costs several wrapped lines in
// the input area to say less than the headline did.
//
// Below everything in this list sit the document's own <title> and then its
// first <h1>, neither of which is a tag anyone wrote for sharing — see
// newPreview. The bare <title> stays below every description. It
// is the one field no author curated for sharing: it carries the section and
// site names that og:title omits ("… | CNN Politics"), and on a templated site
// it is often nothing but those.
var summaryChain = []Field{
	FieldOGTitle, FieldTwitterTitle,
	FieldOGDescription, FieldTwitterDesc, FieldMetaDescription,
}

// friendlyType labels the non-HTML resources worth naming. Anything absent falls
// back to its raw MIME type, which is still more use than silence.
var friendlyType = map[string]string{
	"application/pdf": "PDF",
	"application/zip": "ZIP archive",
	"image/png":       "PNG image",
	"image/jpeg":      "JPEG image",
	"image/gif":       "GIF image",
	"image/svg+xml":   "SVG image",
	"image/webp":      "WebP image",
	"text/plain":      "plain text",
	"video/mp4":       "MP4 video",
	"audio/mpeg":      "MP3 audio",
}

// newPreview resolves parsed <head> metadata into a Preview.
func newPreview(finalURL string, m meta) Preview {
	p := Preview{URL: finalURL, SiteName: m.tags["og:site_name"]}

	// Title and Desc are resolved independently of which one Summary will pick:
	// both describe the page, and a caller that wants to show them side by side
	// (the CLI's -v) needs each filled in regardless.
	p.Title = firstTag(m, FieldOGTitle, FieldTwitterTitle)
	if p.Title == "" {
		p.Title = m.title
	}
	if p.Title == "" {
		p.Title = m.h1
	}
	p.Desc = firstTag(m, FieldOGDescription, FieldTwitterDesc, FieldMetaDescription)

	// Field records Summary's choice. Recording which field is enough for
	// Summary to render it, because Title and Desc each already hold the first
	// match from their own stretch of the chain.
	for _, f := range summaryChain {
		if m.tags[string(f)] != "" {
			p.Field = f
			break
		}
	}
	if p.Field == FieldNone && p.Title != "" {
		// Nothing curated anywhere, so Title holds whatever the document called
		// itself: its <title>, or failing that the heading it opens with.
		if m.title != "" {
			p.Field = FieldTitle
		} else {
			p.Field = FieldH1
		}
	}
	return p
}

// firstTag returns the first non-empty tag among fields, in the order given.
func firstTag(m meta, fields ...Field) string {
	for _, f := range fields {
		if v := m.tags[string(f)]; v != "" {
			return v
		}
	}
	return ""
}

// newTypePreview describes a resource that was never HTML to begin with.
func newTypePreview(finalURL, mediaType string) Preview {
	return Preview{URL: finalURL, ContentType: mediaType, Field: FieldContentType}
}

// Summary renders the preview as a single line of at most maxLen runes, or "" if
// the page offered nothing worth showing. Callers are expected to treat "" as
// "leave the URL alone" rather than substituting a placeholder — a bare URL reads
// better than one annotated with "(no description)".
func (p Preview) Summary(maxLen int) string {
	if p.ContentType != "" {
		return truncate(p.typeSummary(), maxLen)
	}
	return truncate(collapse(p.summaryText()), maxLen)
}

// summaryText returns the text Field points at.
func (p Preview) summaryText() string {
	switch p.Field {
	case FieldOEmbed, FieldOGTitle, FieldTwitterTitle, FieldTitle, FieldH1:
		return p.Title
	case FieldOGDescription, FieldTwitterDesc, FieldMetaDescription:
		return p.Desc
	}
	// Field unset, so this Preview was assembled by hand rather than by
	// newPreview and there is no record of where its text came from. Fall back
	// to the preference the chain encodes: headline, then description.
	if p.Title != "" {
		return p.Title
	}
	return p.Desc
}

// typeSummary names a non-HTML resource, and its filename when the URL has one.
func (p Preview) typeSummary() string {
	label, ok := friendlyType[p.ContentType]
	if !ok {
		label = p.ContentType
	}
	if name := fileName(p.URL); name != "" {
		return label + ": " + name
	}
	return label
}

// fileName returns the last path segment of rawURL, if it looks like a filename.
func fileName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || !strings.Contains(name, ".") {
		return ""
	}
	return name
}

// collapse folds every run of whitespace — including the newlines that wrapped
// content= attributes in the source — into single spaces.
func collapse(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// truncate shortens s to at most maxLen runes, cutting at a word boundary and
// marking the cut with an ellipsis. maxLen <= 0 means no limit.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	// Leave room for the ellipsis itself.
	cut := maxLen - 1
	if cut < 1 {
		return "…"
	}
	// Back off to the last word boundary so we don't end mid-word — but not if
	// that throws away most of the text, as it would for a single long token.
	//
	// The search window is r[:cut+1], one wider than the text we keep: a space
	// sitting exactly at the cut means the kept text already ends on a whole
	// word, and backing off from there would drop a word that fits.
	if i := lastSpace(r[:cut+1]); i > cut/2 {
		cut = i
	}
	return strings.TrimRight(string(r[:cut]), " ") + "…"
}

func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if unicode.IsSpace(r[i]) {
			return i
		}
	}
	return -1
}
