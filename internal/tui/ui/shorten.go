package ui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/urlshorten"
)

// M-s replaces a long URL in the line being composed with a short one, followed
// by the host it stands for in brackets:
//
//	https://s.u13.net/7f2 [arstechnica.com]
//
// That shape comes from tigerlily's cj.pl, which annotated overlong links the
// same way. The bracketed host is the point of it: a short link tells a reader
// nothing about where it goes, and pasting one into a discussion without saying
// is a small act of misdirection. Keeping the host attached costs a few columns
// and keeps the link honest.
//
// The substitution happens in the input line, before anything is sent, so the
// user sees exactly what will go out and can undo it by editing like any other
// text. Nothing is shortened automatically — cj.pl shortened on send, past a
// length threshold, which meant a link could change shape after the user was
// done with it. Here it takes a keystroke, every time.
//
// Like previews (see linkpreview.go), the in-flight request is keyed by URL
// text and occurrence rather than by byte offset: the user goes on typing while
// it is out, and offsets taken before the request go stale on every keystroke
// ahead of them. Re-deriving the span from the current input when the answer
// lands costs one scan and is always right.

// shortenTimeout bounds one shorten request. Longer than a preview's, because
// the user asked for this one by name and is waiting on it, and a shortener
// that is merely slow is still worth waiting for.
const shortenTimeout = 10 * time.Second

// maxShortOriginals bounds the short->original memo. It grows only on M-s, so
// it is small in practice; the cap is there because it survives sends and a
// session can run for weeks.
const maxShortOriginals = 64

// urlOccurrence identifies one occurrence of a URL in the input line.
type urlOccurrence struct {
	url string
	occ int // 0-based index among occurrences of url in the line
}

// shortenResultMsg carries a finished shorten back into Update. short is the
// bare short URL; err is why there is none. The bracketed text that goes into
// the line is composed on arrival, so the URL itself stays available to record
// against the original.
type shortenResultMsg struct {
	url   string
	occ   int
	short string
	err   error
}

// maybeShortenHint prints the one-time reminder that M-s exists, the first time
// the user has a URL in the line they are composing.
//
// It fires on the input line rather than on URLs arriving in scrollback,
// because that is the only place M-s can act: a reminder offered against
// somebody else's link would name a key that does nothing to it. And it fires
// as soon as a URL appears rather than waiting for one to be finished the way
// previews do — a reminder is useful while the URL is still being worked on,
// and unlike a preview fetch, being early costs nothing and reaches no network.
//
// Once per session, on the theory that the reminder is for people who have not
// met the feature; anyone who has stops needing it after the first time.
func (m Model) maybeShortenHint() Model {
	if m.shortenHintShown || m.inputValue == "" {
		return m
	}
	if len(inputURLSpans(m.inputValue)) == 0 {
		return m
	}
	m.shortenHintShown = true
	m.output = append(m.output, OutputItem{Type: "command", Data: []string{
		"Tip: M-s shortens the first URL before the cursor, keeping the site",
		"name in brackets after it. See '%help shorten'. (Shown once a session.)",
	}})
	return m.syncViewportContent()
}

// shortener returns the service M-s will use.
//
// The Model holds the service by name rather than by value so that its zero
// value is usable: a Model built without going through New (as the tests do)
// would otherwise carry a nil Service and panic on the first M-s.
func (m Model) shortener() urlshorten.Service {
	if svc, ok := urlshorten.Lookup(m.shortenService); ok {
		return svc
	}
	return urlshorten.Default()
}

// handleShortenCommand implements %shorten [service].
func (m Model) handleShortenCommand(fields []string) (Model, []string) {
	switch len(fields) {
	case 1:
		return m, []string{
			"URL shortener: " + m.shortener().Name(),
			"Available: " + strings.Join(urlshorten.Names(), ", "),
		}
	case 2:
		svc, ok := urlshorten.Lookup(fields[1])
		if !ok {
			return m, []string{
				"No such shortener: " + fields[1],
				"Usage: %shorten " + strings.Join(urlshorten.Names(), "|"),
			}
		}
		m.shortenService = svc.Name()
		return m, []string{"URL shortener: " + svc.Name()}
	}
	return m, []string{"Usage: %shorten " + strings.Join(urlshorten.Names(), "|")}
}

// shortenURLAtCursor starts a shorten for the URL the user is working on, and
// reports when there is nothing to shorten.
func (m Model) shortenURLAtCursor() (Model, tea.Cmd) {
	sp, ok, short := firstURLBeforeCursor(m.inputValue, m.inputCursor)
	switch {
	case !ok:
		return m.noteShorten("No URL found before the cursor."), nil
	case short:
		// Shortening a short link works — the service would happily mint a
		// second id pointing at the first — but the result is a link that is no
		// shorter, has lost the host it named, and takes two hops to follow.
		return m.noteShorten("URL is already shortened."), nil
	}

	key := urlOccurrence{url: sp.url, occ: sp.occ}
	if m.shortenPending[key] {
		// A second press while the first is out would mint a second short URL
		// for the same link and then race to substitute it.
		return m, nil
	}
	if m.shortenPending == nil {
		m.shortenPending = make(map[urlOccurrence]bool)
	}
	m.shortenPending[key] = true
	return m, shortenCmd(m.shortener(), sp.url, sp.occ)
}

// firstURLBeforeCursor returns the first URL in s that starts before cursor,
// whether it is already a short link, and whether there was one at all.
//
// "Before the cursor" is measured from the URL's start rather than its end, so
// that the URL the cursor is sitting in or at the end of — which is where it is
// the moment one has been typed or pasted — counts as being before it.
//
// A short link is reported rather than skipped: with two links in the line, the
// first is what the user pointed at, and silently shortening the second instead
// would rewrite a part of the line they were not looking at.
func firstURLBeforeCursor(s string, cursor int) (sp urlSpan, found, short bool) {
	for _, span := range inputURLSpans(s) {
		if span.start < cursor {
			return span, true, urlshorten.IsShort(span.url)
		}
	}
	return urlSpan{}, false, false
}

// findURLOccurrence locates one URL occurrence in s as it stands now.
func findURLOccurrence(s, rawURL string, occ int) (urlSpan, bool) {
	for _, sp := range inputURLSpans(s) {
		if sp.url == rawURL && sp.occ == occ {
			return sp, true
		}
	}
	return urlSpan{}, false
}

// applyShortenResult substitutes a finished shorten into the input line, or
// reports why it could not.
func (m Model) applyShortenResult(msg shortenResultMsg) Model {
	delete(m.shortenPending, urlOccurrence{url: msg.url, occ: msg.occ})

	if msg.err != nil {
		// The long URL stays exactly where it was. A shortener that is down or
		// refusing must not cost the user the link they typed.
		m.output = append(m.output, OutputItem{Type: "error", Data: "shorten: " + msg.err.Error()})
		return m.syncViewportContent()
	}

	sp, ok := findURLOccurrence(m.inputValue, msg.url, msg.occ)
	if !ok {
		// The line moved on while the request was out — sent, cleared, or the
		// URL edited past recognition. There is nowhere to put the short URL
		// that the user would thank us for, so it is dropped.
		return m
	}
	// Remember what this link stands for before it goes in. A short link cannot
	// be read by fetching it — see previewTarget — and this is the one moment
	// when the answer is free.
	m = m.rememberShortened(msg.short, msg.url)

	text := shortenReplacement(msg.short, msg.url)
	m.inputValue = m.inputValue[:sp.start] + text + m.inputValue[sp.end:]
	m.inputCursor = cursorAfterSubstitution(m.inputCursor, sp, len(text))
	return m
}

// rememberShortened records that shortURL stands for original, and carries the
// original's link preview over to it.
//
// Carrying the preview over is what makes the substitution seamless: the URL
// the user just shortened had usually been previewed already, so the ghost text
// simply stays put instead of blinking out and being fetched again — against a
// link that, fetched directly, would describe the shortener.
func (m Model) rememberShortened(shortURL, original string) Model {
	if m.shortOriginals == nil {
		m.shortOriginals = make(map[string]string)
	}
	if len(m.shortOriginals) >= maxShortOriginals {
		// Bounded like the preview memo, and for the same reason: this survives
		// sends, so in a long session it would otherwise only ever grow. A
		// dropped entry costs one reverse lookup, not a wrong preview.
		for k := range m.shortOriginals {
			delete(m.shortOriginals, k)
			if len(m.shortOriginals) < maxShortOriginals*3/4 {
				break
			}
		}
	}
	m.shortOriginals[shortURL] = original

	if summary, ok := m.linkPreviewCache[original]; ok {
		if m.linkPreviewCache == nil {
			m.linkPreviewCache = make(map[string]string)
		}
		m.linkPreviewCache[shortURL] = summary
	}
	return m
}

// cursorAfterSubstitution places the cursor once sp has been replaced by n
// bytes of new text.
func cursorAfterSubstitution(cursor int, sp urlSpan, n int) int {
	switch {
	case cursor <= sp.start:
		return cursor // ahead of the change; nothing moved under it
	case cursor >= sp.end:
		return cursor + n - (sp.end - sp.start) // behind it; rides along
	default:
		// Inside the old URL, where no offset survives the swap. The end of the
		// replacement is where the user would have put it themselves.
		return sp.start + n
	}
}

// noteShorten prints a one-line report, in the same voice as the other keys
// that have something to say for themselves (M-m, %linkpreview).
func (m Model) noteShorten(note string) Model {
	m.output = append(m.output, OutputItem{Type: "command", Data: []string{note}})
	return m.syncViewportContent()
}

// shortenCmd runs one shorten off the UI goroutine.
//
// The service is passed in rather than re-read from the Model, because this
// closure outlives the keystroke that made it: %shorten could select a
// different one while the request is out, and the answer that comes back is the
// one this service gave.
func shortenCmd(svc urlshorten.Service, rawURL string, occ int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shortenTimeout)
		defer cancel()
		short, err := svc.Shorten(ctx, rawURL)
		if err != nil {
			return shortenResultMsg{url: rawURL, occ: occ, err: fmt.Errorf("%s: %w", svc.Name(), err)}
		}
		return shortenResultMsg{url: rawURL, occ: occ, short: short}
	}
}

// shortenReplacement is the text that goes into the input line: the short URL,
// then the original's host in brackets.
func shortenReplacement(short, original string) string {
	u, err := url.Parse(original)
	if err != nil || u.Hostname() == "" {
		// Nothing to name. inputURLSpans only matches things with a scheme and
		// a host, so this is unreachable in practice — but a bare short URL is
		// still a working substitution, and better than a stray "[]".
		return short
	}
	return short + " [" + u.Hostname() + "]"
}
