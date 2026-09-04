package ui

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/cmdarg"
	"github.com/joshw/zephyrlily/internal/linkpreview"
	"github.com/joshw/zephyrlily/internal/tui/ascify"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/urlshorten"
)

// Link previews annotate a URL in the input line with what the page says about
// itself, drawn in gray after the URL as text that is not yet really there.
//
// The ghost text is deliberately kept OUT of inputValue. Everything that edits
// the input — the three dozen call sites in input.go, plus completion and
// expansion — works on inputValue in byte offsets, and the send path reads it
// verbatim. Splicing preview text in there would mean teaching every one of
// those to skip it, and any miss would put text the user never wrote into a
// sent message. Instead previews live beside the input and are woven in only
// when the input area is drawn (see inputDisplay), so an unaccepted preview
// cannot reach the wire no matter what path a message takes. Tab accepts them
// by splicing them into inputValue for real, at which point they are ordinary
// text and stop being special. A URL that resolved to nothing shows a
// "no preview available" placeholder instead, which Tab steps over: it exists
// to report that the lookup happened, and can never become message text.
//
// Previews are keyed by URL text rather than by byte offset. Offsets go stale
// on every insertion before them and would need fixing up in each of those edit
// paths; re-deriving the anchors from the current inputValue at draw time costs
// one scan of a single line and is always right. It also gives editing a URL
// the correct behaviour for free — the text no longer matches, so its preview
// stops showing.

const (
	// maxPreviewLen caps a summary in characters — most of a typical
	// og:description, rather than the first sentence of one. The input area
	// cannot run away with it: calculateInputHeight already refuses to take
	// more than half the window, so a long preview costs a few wrapped lines
	// and no more.
	maxPreviewLen = 256

	// previewTimeout bounds one preview fetch, over the package's own default.
	previewTimeout = 5 * time.Second

	// noPreviewText stands in when a URL resolved to nothing worth showing, so
	// that a link the user pasted visibly reports "asked, and there was
	// nothing" rather than looking indistinguishable from one still in flight.
	// It is never acceptable and so can never reach a message.
	noPreviewText = "no preview available"

	// maxPreviewCache bounds the url→summary memo. It survives sends so that
	// re-pasting a link is instant, which means it would otherwise grow for as
	// long as the session lives.
	maxPreviewCache = 64
)

// linkPreviewResultMsg carries a finished fetch back into Update. A summary of
// "" means the page offered nothing worth showing; that is a normal outcome,
// not an error, and it is recorded so the URL is not fetched again.
type linkPreviewResultMsg struct {
	url     string
	summary string
}

// previewKey identifies one occurrence of a URL in the input line, so that
// dismissing a preview on a line that repeats the same link only dismisses the
// one the cursor is on.
type previewKey struct {
	url string
	occ int // 0-based index among occurrences of url in the line
}

// urlSpan is one URL found in the input line.
type urlSpan struct {
	start, end int    // byte offsets into inputValue; end excludes trailing punctuation
	url        string // the cleaned URL text
	occ        int    // occurrence index of this url within the line
}

// ghost is a preview positioned for drawing: text to show at a byte offset.
type ghost struct {
	at   int // byte offset into inputValue, immediately after the URL
	text string
	key  previewKey

	// placeholder marks the "no preview available" stand-in. It draws like any
	// other ghost but Tab steps over it, so there is no sequence of keys that
	// puts it into inputValue and therefore none that sends it.
	placeholder bool
}

// inputURLSpans finds every URL in s, in order, with trailing sentence
// punctuation trimmed the same way the scrollback linkifier trims it.
func inputURLSpans(s string) []urlSpan {
	// Fast path for the overwhelmingly common case. This runs on every
	// keystroke and every draw of the input area; a substring check costs
	// nothing next to starting the regexp engine on a line with no link in it.
	if !strings.Contains(s, "://") {
		return nil
	}
	locs := urlPattern.FindAllStringIndex(s, -1)
	if locs == nil {
		return nil
	}
	seen := make(map[string]int, len(locs))
	spans := make([]urlSpan, 0, len(locs))
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		for end > start && strings.IndexByte(trailingURLPunct, s[end-1]) != -1 {
			end--
		}
		if end <= start {
			continue
		}
		url := s[start:end]
		spans = append(spans, urlSpan{start: start, end: end, url: url, occ: seen[url]})
		seen[url]++
	}
	return spans
}

// ghosts returns the previews to draw against the current input line, in
// ascending offset order. A URL contributes one only if a summary has come back
// for it, it has not been dismissed, and previews are switched on at all.
func (m Model) ghosts() []ghost {
	if !m.linkPreviewOn || len(m.linkPreviewCache) == 0 || m.inputValue == "" {
		return nil
	}
	var out []ghost
	for _, sp := range inputURLSpans(m.inputValue) {
		summary, fetched := m.linkPreviewCache[sp.url]
		if !fetched {
			// Not asked yet, or still in flight. Silence is right here: the
			// placeholder means "we looked and there was nothing", which is not
			// yet true.
			continue
		}
		key := previewKey{url: sp.url, occ: sp.occ}
		if m.linkPreviewDismissed[key] {
			continue
		}
		g := ghost{at: sp.end, text: " (" + summary + ")", key: key}
		if summary == "" {
			g.text, g.placeholder = " ("+noPreviewText+")", true
		}
		if alreadyOffered(m.inputValue, g.text) {
			// Nothing left to offer: this text is in the line already.
			continue
		}
		out = append(out, g)
	}
	return out
}

// alreadyOffered reports whether the line already carries this preview's text,
// in which case there is nothing to offer and the ghost is not drawn.
//
// The dismissal set does not cover this on its own, because it is keyed by URL
// and the text can outlive the URL it came from. Accepting a preview and then
// shortening the URL is the case that showed it up: the accepted "(Title)" stays
// in the line while the URL under it is replaced by a short one, which is a
// different key with the same summary carried over — so the line ended up
// offering "(Title)" a second time, right next to the copy the user had already
// taken.
//
// The whole line is searched rather than the text just after the URL, because
// that is not where the accepted copy ends up: shortening leaves the bracketed
// host between the URL and its preview.
func alreadyOffered(line, ghostText string) bool {
	// Matched without the leading space, so that a preview whose spacing was
	// edited after being accepted still counts as present.
	return strings.Contains(line, strings.TrimPrefix(ghostText, " "))
}

// inputDisplay returns the input line as it should be drawn — inputValue with
// every ghost's text spliced in — together with the ghosts' spans in the
// returned string's own coordinates.
//
// Everything that measures or draws the input area works over this string, so
// that a preview occupies real columns: it wraps the line, grows the input
// area, and shifts the cursor exactly as typed text would.
func (m Model) inputDisplay() (string, []ghostSpan) {
	gs := m.ghosts()
	if len(gs) == 0 {
		return m.inputValue, nil
	}
	var sb strings.Builder
	sb.Grow(len(m.inputValue) + len(gs)*32)
	spans := make([]ghostSpan, 0, len(gs))
	prev := 0
	for _, g := range gs {
		sb.WriteString(m.inputValue[prev:g.at])
		spans = append(spans, ghostSpan{start: sb.Len(), end: sb.Len() + len(g.text), key: g.key})
		sb.WriteString(g.text)
		prev = g.at
	}
	sb.WriteString(m.inputValue[prev:])
	return sb.String(), spans
}

// ghostSpan marks a stretch of the display string that is preview text rather
// than input the user typed.
type ghostSpan struct {
	start, end int // byte offsets into the display string
	key        previewKey
}

// contains reports whether display offset off falls inside the span.
func (g ghostSpan) contains(off int) bool { return off >= g.start && off < g.end }

// toDisplay maps a byte offset in inputValue to the matching offset in the
// display string. An offset sitting exactly where a ghost is anchored maps to
// just before the ghost: the cursor belongs at the end of the URL it typed, not
// past preview text that is not really there.
func toDisplay(gs []ghost, off int) int {
	d := off
	for _, g := range gs {
		if g.at < off {
			d += len(g.text)
		}
	}
	return d
}

// toInput maps a byte offset in the display string back to inputValue. An
// offset inside a ghost maps to that ghost's anchor, so a click on preview text
// puts the cursor at the end of the URL it describes.
func toInput(gs []ghost, off int) int {
	shift := 0
	for _, g := range gs {
		gStart := g.at + shift // this ghost's start, in display coordinates
		if off < gStart {
			break
		}
		if off < gStart+len(g.text) {
			return g.at
		}
		shift += len(g.text)
	}
	in := off - shift
	if in < 0 {
		in = 0
	}
	return in
}

// previewCmds returns fetches to start for URLs in the current input line.
//
// force fetches every URL in the line, which is what a paste wants: the text
// arrived complete. Without it only URLs that something follows are fetched, so
// that typing does not chase a URL through every prefix it passes on the way to
// being finished — which would fetch garbage, and leak each half-typed address
// to whatever host it happened to name.
func (m Model) previewCmds(force bool) (Model, []tea.Cmd) {
	if !m.linkPreviewOn {
		return m, nil
	}
	var cmds []tea.Cmd
	for _, sp := range inputURLSpans(m.inputValue) {
		if !force && sp.end >= len(m.inputValue) {
			continue // nothing after it yet; still being typed
		}
		if _, done := m.linkPreviewCache[sp.url]; done {
			continue
		}
		if m.linkPreviewPending[sp.url] {
			continue
		}
		if m.linkPreviewPending == nil {
			m.linkPreviewPending = make(map[string]bool)
		}
		m.linkPreviewPending[sp.url] = true
		cmds = append(cmds, fetchPreviewCmd(m.client, sp.url, m.shortOriginals[sp.url]))
	}
	return m, cmds
}

// echoesHost reports whether a summary tells the reader nothing the URL already
// told them — "Reddit" for reddit.com, "GitHub" for github.com, "Go" for go.dev.
// Site roots and aggregators very often publish exactly this as their og:title,
// and appending it to the link is pure noise.
//
// The test is deliberately narrow: the summary must reduce to a substring of the
// host itself. A real description cannot pass, because it is far longer than a
// hostname — so "Welcome to Cloudflare - Powering the next generation of
// applications" survives on cloudflare.com while a bare "Cloudflare" would not.
//
// This judgment lives here rather than in internal/linkpreview because that
// package's job is to report what a page says about itself, not to rank it;
// whether a given summary is worth screen space is the caller's call, and the
// CLI deliberately wants to see the ones this would drop.
func echoesHost(summary, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := foldAlnum(strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."))
	text := foldAlnum(summary)
	if host == "" || text == "" {
		return false
	}
	return strings.Contains(host, text)
}

// foldAlnum reduces s to its lower-case letters and digits, so that "Hacker
// News", "hacker-news" and "hackernews" all compare equal, and a hostname's
// dots stop standing between it and a title that spells it out.
func foldAlnum(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// previewText renders a fetched summary in the only alphabet the input line
// deals in: pure ASCII.
//
// Page metadata is full of typographic Unicode — curly quotes, em dashes, an
// ellipsis from truncation, the odd emoji — and both halves of this client
// reject it. Lily's wire is ASCII, so anything else is stripped on send and the
// accepted text arrives mangled. Worse, renderInputArea hard-wraps by byte
// offset on the assumption that one byte is one display column (see
// insertString), which a multi-byte rune quietly breaks: the ghost text would
// wrap somewhere other than where the terminal draws it.
//
// So preview text goes through exactly what typed and pasted text goes
// through. Doing it here, once per fetch, means what is drawn is already what
// would be sent — the gray text is a faithful preview of the real thing rather
// than something that changes shape on the way out.
func previewText(summary string) string {
	s := stripControl(ascify.String(summary))
	// ascify can lengthen what it converts — "…" becomes "...", an emoji its
	// bracketed name — so the cap is re-applied to the converted form. Plain
	// byte arithmetic is safe now that the string is ASCII.
	if len(s) > maxPreviewLen {
		s = strings.TrimRight(s[:maxPreviewLen-3], " ") + "..."
	}
	return s
}

// summaryFor renders a fetched page as the line to show, falling through the
// fallback chain when the field it picked says nothing the URL had not already
// said.
//
// The fall-through exists because the chain leads with og:title, which on an
// article is the headline but on a site root is just the masthead — "Ars
// Technica" for arstechnica.com. Dropping that outright would have made the
// site root report no preview at all, when its description ("News and reviews,
// covering IT, AI, science…") is a perfectly good blurb sitting one step
// further down the chain.
func summaryFor(p linkpreview.Preview, requestedURL string) string {
	// Both the URL asked for and the one it ended at: a shortener can echo
	// either, and only the final URL names the site actually reached.
	echoes := func(s string) bool {
		return s != "" && (echoesHost(s, p.URL) || echoesHost(s, requestedURL))
	}

	// Converted before the echo test so the comparison sees the same text the
	// user will, and before caching so it is done once per URL.
	summary := previewText(p.Summary(maxPreviewLen))
	if !echoes(summary) {
		return summary
	}
	if isTitleField(p.Field) && p.Desc != "" {
		// Re-render with Field pointed at the description. Going back through
		// Summary rather than reading Desc directly keeps the whitespace
		// collapsing and word-boundary truncation identical.
		alt := p
		alt.Field = linkpreview.FieldOGDescription
		if cand := previewText(alt.Summary(maxPreviewLen)); !echoes(cand) {
			return cand
		}
	}
	return ""
}

// isTitleField reports whether f names one of the headline fields, the ones a
// description can be tried in place of.
func isTitleField(f linkpreview.Field) bool {
	switch f {
	case linkpreview.FieldOGTitle, linkpreview.FieldTwitterTitle, linkpreview.FieldTitle:
		return true
	}
	return false
}

// fetchPreviewCmd resolves one URL off the UI goroutine.
//
// known is the URL rawURL was shortened from, when this session is the one that
// shortened it; "" means we do not already know.
func fetchPreviewCmd(c *client.Client, rawURL, known string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
		defer cancel()
		target, ok := previewTarget(ctx, c, rawURL, known)
		if !ok {
			// A short link we could not see through. Previewing it anyway would
			// describe the shortener rather than the destination, which is
			// worse than saying nothing: "The da.gd URL shortening service" next
			// to a da.gd link tells the reader precisely nothing.
			return linkPreviewResultMsg{url: rawURL}
		}
		p, err := fetchPreview(ctx, c, target)
		if err != nil {
			// A URL we cannot reach simply gets no preview. Reporting the
			// failure would put noise in the scrollback for something the user
			// never explicitly asked for.
			return linkPreviewResultMsg{url: rawURL}
		}
		// Keyed by the URL in the input line, but summarised — and echo-tested
		// — against the page actually reached.
		return linkPreviewResultMsg{url: rawURL, summary: summaryFor(p, target)}
	}
}

// previewTarget returns the URL whose page describes rawURL.
//
// For an ordinary URL that is rawURL itself. For a short link it is what the
// link points at, because fetching a short link does not reach the destination:
// da.gd answers a browser-shaped Accept header — which is exactly what the
// preview fetcher sends, to get the metadata real sites serve browsers — with a
// click-through interstitial of its own, served as a 200. Following redirects
// does not help, because there is no redirect to follow.
//
// A link this session shortened needs no lookup at all: M-s knew the original
// and wrote it down. Anything else is put to the service that issued it (da.gd
// documents "+" for exactly this), which costs one small request against a host
// that has nothing else to tell us.
func previewTarget(ctx context.Context, c *client.Client, rawURL, known string) (string, bool) {
	if known != "" {
		return known, true
	}
	if !urlshorten.IsShort(rawURL) {
		return rawURL, true
	}
	long, err := expandShortURL(ctx, c, rawURL)
	if err != nil {
		return "", false
	}
	return long, true
}

// applyPreviewResult records a finished fetch. Empty summaries are cached too,
// so a page with no metadata is asked once rather than on every trigger.
func (m Model) applyPreviewResult(msg linkPreviewResultMsg) Model {
	delete(m.linkPreviewPending, msg.url)
	if m.linkPreviewCache == nil {
		m.linkPreviewCache = make(map[string]string)
	}
	if len(m.linkPreviewCache) >= maxPreviewCache {
		// Drop arbitrary entries rather than tracking recency: this is a
		// latency memo, and a miss costs one refetch.
		for k := range m.linkPreviewCache {
			delete(m.linkPreviewCache, k)
			if len(m.linkPreviewCache) < maxPreviewCache*3/4 {
				break
			}
		}
	}
	m.linkPreviewCache[msg.url] = msg.summary
	return m
}

// acceptPreviews splices every showing preview into inputValue, turning ghost
// text into ordinary text that will be sent. The cursor keeps its place in the
// text, shifting by whatever was inserted ahead of it.
func (m Model) acceptPreviews() (Model, bool) {
	// Placeholders are not text — there is nothing to accept — so they are
	// filtered out before anything is spliced. Reporting false when only
	// placeholders are showing is what lets Tab fall through to completion
	// instead of going dead while one is on screen.
	var gs []ghost
	for _, g := range m.ghosts() {
		if !g.placeholder {
			gs = append(gs, g)
		}
	}
	if len(gs) == 0 {
		return m, false
	}
	var sb strings.Builder
	cursor := m.inputCursor
	prev := 0
	for _, g := range gs {
		sb.WriteString(m.inputValue[prev:g.at])
		sb.WriteString(g.text)
		// >= not >: a cursor resting exactly where the preview is anchored (the
		// end of the URL, which is where it is after typing one) belongs after
		// the text just accepted, not stranded in front of it.
		if m.inputCursor >= g.at {
			cursor += len(g.text)
		}
		prev = g.at
	}
	sb.WriteString(m.inputValue[prev:])
	m.inputValue = sb.String()
	m.inputCursor = cursor
	// Accepted text is the user's now. Clearing the cache entries would make a
	// later retype refetch, so only the dismissal set is reset — the URLs are
	// still in the line, and without this their previews would immediately
	// reappear after the copy that was just accepted.
	m = m.suppressAll(gs)
	return m, true
}

// suppressAll marks the given previews as dismissed, so they stop showing
// without being forgotten.
func (m Model) suppressAll(gs []ghost) Model {
	if m.linkPreviewDismissed == nil {
		m.linkPreviewDismissed = make(map[previewKey]bool)
	}
	for _, g := range gs {
		m.linkPreviewDismissed[g.key] = true
	}
	return m
}

// dismissPreviewAtCursor removes the one preview the cursor is adjacent to,
// and reports whether it did.
//
// The cursor lives in inputValue coordinates, where a preview occupies no
// space at all — it sits at a single offset, the end of its URL. So the only
// position from which a preview can be dismissed is that offset, which is both
// "immediately after the URL" and, on screen, immediately before the preview
// text. Backspace there removes the preview instead of deleting a character;
// a second Backspace then edits the URL as usual.
func (m Model) dismissPreviewAtCursor() (Model, bool) {
	for _, g := range m.ghosts() {
		if g.at == m.inputCursor {
			if m.linkPreviewDismissed == nil {
				m.linkPreviewDismissed = make(map[previewKey]bool)
			}
			m.linkPreviewDismissed[g.key] = true
			return m, true
		}
	}
	return m, false
}

// resetPreviewsForNewLine clears per-line state after a send. The url→summary
// memo deliberately survives: it is not tied to any one line, and keeping it
// makes re-sending the same link instant.
func (m Model) resetPreviewsForNewLine() Model {
	m.linkPreviewDismissed = nil
	return m
}

// handleLinkPreviewCommand implements %linkpreview [on|off].
func (m Model) handleLinkPreviewCommand(fields []string) (Model, []string) {
	on, ok := false, false
	if len(fields) == 2 {
		on, ok = cmdarg.OnOff(fields[1])
	}
	switch {
	case ok:
		m.linkPreviewOn = on
		if !on {
			// Stop drawing immediately; anything in flight lands in the cache
			// and simply is not shown.
			m.linkPreviewDismissed = nil
		}
		return m, []string{"Link previews: " + onOff(on)}
	case len(fields) == 1:
		return m, []string{"Link previews: " + onOff(m.linkPreviewOn)}
	}
	return m, []string{"Usage: %linkpreview on|off"}
}
