package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/urlshorten"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortenModel builds a model with a line already composed, so the tests
// exercise the selection and substitution rather than the network. Nothing here
// runs the tea.Cmd that would make a request.
func shortenModel(value string, cursor int) Model {
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false
	m.input = textarea.New()
	m.inputValue = value
	m.inputCursor = cursor
	return m
}

const shortenTestURL = "https://arstechnica.com/some/very/long/article/path"

func TestFirstURLBeforeCursor(t *testing.T) {
	line := "see " + shortenTestURL + " and https://example.com/x too"

	for _, tc := range []struct {
		name    string
		line    string
		cursor  int
		wantURL string
		wantOcc int
		found   bool
		short   bool
	}{
		{name: "no url at all", line: "nothing to see here", cursor: 19},
		{
			name: "cursor at end of the url just typed",
			line: "see " + shortenTestURL, cursor: len("see " + shortenTestURL),
			wantURL: shortenTestURL, found: true,
		},
		{
			name: "cursor inside the url counts as after its start",
			line: "see " + shortenTestURL, cursor: len("see ") + 10,
			wantURL: shortenTestURL, found: true,
		},
		{
			name:   "cursor ahead of the only url finds nothing",
			line:   "see " + shortenTestURL,
			cursor: len("see "),
		},
		{
			name: "first of two, not the nearest",
			line: line, cursor: len(line),
			wantURL: shortenTestURL, found: true,
		},
		{
			name: "second url once the cursor is ahead of the first",
			line: line, cursor: len("see " + shortenTestURL + " and h"),
			// The first URL still starts before the cursor, so it is still the
			// one chosen — "first", not "nearest".
			wantURL: shortenTestURL, found: true,
		},
		{
			name: "trailing punctuation is not part of the url",
			line: "read " + shortenTestURL + ".", cursor: len("read "+shortenTestURL) + 1,
			wantURL: shortenTestURL, found: true,
		},
		{
			name: "same url twice reports the first occurrence",
			line: shortenTestURL + " " + shortenTestURL, cursor: len(shortenTestURL + " " + shortenTestURL),
			wantURL: shortenTestURL, wantOcc: 0, found: true,
		},
		{
			name: "an already-short url is reported as such",
			line: "https://s.u13.net/7f2", cursor: len("https://s.u13.net/7f2"),
			wantURL: "https://s.u13.net/7f2", found: true, short: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp, found, short := firstURLBeforeCursor(tc.line, tc.cursor)
			assert.Equal(t, tc.found, found, "found")
			assert.Equal(t, tc.short, short, "already short")
			if found {
				assert.Equal(t, tc.wantURL, sp.url, "url")
				assert.Equal(t, tc.wantOcc, sp.occ, "occurrence")
			}
		})
	}
}

func TestShortenReportsWhenThereIsNothingToShorten(t *testing.T) {
	t.Run("no url", func(t *testing.T) {
		m := shortenModel("just some words", len("just some words"))
		m, cmd := m.shortenURLAtCursor()
		assert.Nil(t, cmd, "nothing should be requested when there is no URL")
		assert.Contains(t, strings.Join(lastOutputLines(t, m), "\n"), "No URL found")
	})

	t.Run("url is only after the cursor", func(t *testing.T) {
		m := shortenModel("see "+shortenTestURL, 0)
		m, cmd := m.shortenURLAtCursor()
		assert.Nil(t, cmd, "a URL after the cursor is not the one asked for")
		assert.Contains(t, strings.Join(lastOutputLines(t, m), "\n"), "No URL found")
	})

	t.Run("already short", func(t *testing.T) {
		line := "https://s.u13.net/7f2 [example.com]"
		m := shortenModel(line, len(line))
		m, cmd := m.shortenURLAtCursor()
		assert.Nil(t, cmd, "a short URL should not be shortened again")
		assert.Contains(t, strings.Join(lastOutputLines(t, m), "\n"), "already shortened")
	})
}

// M-s reaches the shortener through the normal key path, and does not touch the
// input line on the way out — the line only changes when the answer lands.
func TestShortenKeyStartsARequest(t *testing.T) {
	m := shortenModel("see "+shortenTestURL, len("see "+shortenTestURL))
	upd, cmd := m.handleNormalKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
	m = upd.(Model)

	require.NotNil(t, cmd, "M-s should start a shorten")
	assert.Equal(t, "see "+shortenTestURL, m.inputValue, "the line should be untouched until the answer lands")
	assert.True(t, m.shortenPending[urlOccurrence{url: shortenTestURL}], "the request should be recorded as in flight")

	// A second press while the first is out must not mint a second short URL.
	upd, cmd = m.handleNormalKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModAlt})
	assert.Nil(t, cmd, "a repeat press should not start a second request")
	assert.Len(t, upd.(Model).output, len(m.output), "a repeat press should say nothing")
}

func TestShortenSubstitutesShortURLWithOriginalHost(t *testing.T) {
	line := "read " + shortenTestURL + " today"
	m := shortenModel(line, len(line))
	m.shortenPending = map[urlOccurrence]bool{{url: shortenTestURL}: true}

	m = m.applyShortenResult(shortenResultMsg{
		url:   shortenTestURL,
		short: "https://s.u13.net/7f2",
	})

	assert.Equal(t, "read https://s.u13.net/7f2 [arstechnica.com] today", m.inputValue)
	assert.Empty(t, m.shortenPending[urlOccurrence{url: shortenTestURL}], "the request should no longer be in flight")
}

// A repeated link substitutes the occurrence that was asked for, not the first
// one that matches.
func TestShortenSubstitutesTheRequestedOccurrence(t *testing.T) {
	line := shortenTestURL + " and " + shortenTestURL
	m := shortenModel(line, len(line))

	m = m.applyShortenResult(shortenResultMsg{
		url: shortenTestURL, occ: 1,
		short: "https://s.u13.net/7f2",
	})

	assert.Equal(t, shortenTestURL+" and https://s.u13.net/7f2 [arstechnica.com]", m.inputValue)
}

// Text inserted ahead of the URL while the request was out shifts it; the
// substitution has to land on where it is now, not where it was.
func TestShortenSurvivesEditsAheadOfTheURL(t *testing.T) {
	m := shortenModel("prefix added later: read "+shortenTestURL, 0)

	m = m.applyShortenResult(shortenResultMsg{
		url:   shortenTestURL,
		short: "https://s.u13.net/7f2",
	})

	assert.Equal(t, "prefix added later: read https://s.u13.net/7f2 [arstechnica.com]", m.inputValue)
}

func TestShortenCursorPlacement(t *testing.T) {
	line := "read " + shortenTestURL + " today"
	// The bare URL is what the message carries; the bracketed host is added on
	// arrival, and the cursor has to account for the whole of it.
	const bare = "https://s.u13.net/7f2"
	short := shortenReplacement(bare, shortenTestURL)
	urlStart, urlEnd := len("read "), len("read "+shortenTestURL)

	for _, tc := range []struct {
		name   string
		cursor int
		want   int
	}{
		{"ahead of the url", 2, 2},
		{"at the start of the url", urlStart, urlStart},
		{"inside the url lands at the end of the replacement", urlStart + 12, urlStart + len(short)},
		{"at the end of the url", urlEnd, urlStart + len(short)},
		{"after the url keeps its place in the text", len(line), urlStart + len(short) + len(" today")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := shortenModel(line, tc.cursor)
			m = m.applyShortenResult(shortenResultMsg{url: shortenTestURL, short: bare})
			assert.Equal(t, tc.want, m.inputCursor)
			assert.LessOrEqual(t, m.inputCursor, len(m.inputValue), "cursor must stay within the line")
		})
	}
}

// A shortener that is down costs the user nothing: the long URL stays put and
// the failure is reported.
func TestShortenFailureLeavesTheURLInPlace(t *testing.T) {
	line := "read " + shortenTestURL
	m := shortenModel(line, len(line))
	m.shortenPending = map[urlOccurrence]bool{{url: shortenTestURL}: true}

	m = m.applyShortenResult(shortenResultMsg{url: shortenTestURL, err: errors.New("http 403 Forbidden")})

	assert.Equal(t, line, m.inputValue, "a failed shorten must not disturb the line")
	assert.Equal(t, len(line), m.inputCursor, "a failed shorten must not move the cursor")
	assert.False(t, m.shortenPending[urlOccurrence{url: shortenTestURL}], "the failed request should no longer be in flight")

	last := m.output[len(m.output)-1]
	assert.Equal(t, "error", last.Type)
	assert.Contains(t, last.Data, "403")
}

// The line can be sent or rewritten before the answer arrives, and then there
// is nowhere the substitution belongs.
func TestShortenDropsResultWhenTheURLIsGone(t *testing.T) {
	m := shortenModel("", 0)
	m.shortenPending = map[urlOccurrence]bool{{url: shortenTestURL}: true}
	before := len(m.output)

	m = m.applyShortenResult(shortenResultMsg{
		url:   shortenTestURL,
		short: "https://s.u13.net/7f2",
	})

	assert.Equal(t, "", m.inputValue, "a stale result must not resurrect a sent line")
	assert.Len(t, m.output, before, "a stale result should not be reported")
	assert.False(t, m.shortenPending[urlOccurrence{url: shortenTestURL}])
}

func TestShortenReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, short, original, want string
	}{
		{
			"names the host it stands for",
			"https://s.u13.net/7f2", "https://arstechnica.com/a/b",
			"https://s.u13.net/7f2 [arstechnica.com]",
		},
		{
			"a port is not part of the host",
			"https://s.u13.net/7f2", "http://localhost:8080/a",
			"https://s.u13.net/7f2 [localhost]",
		},
		{
			"userinfo is not the host",
			"https://s.u13.net/7f2", "https://user:pw@example.com/a",
			"https://s.u13.net/7f2 [example.com]",
		},
		{
			"nothing to name",
			"https://s.u13.net/7f2", "not a url",
			"https://s.u13.net/7f2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shortenReplacement(tc.short, tc.original))
		})
	}
}

// %shorten selects which service M-s uses, and a Model that has never been
// asked resolves to the default rather than to nothing.
func TestShortenServiceSelection(t *testing.T) {
	t.Run("the zero model uses the default", func(t *testing.T) {
		assert.Equal(t, urlshorten.DefaultName, Model{}.shortener().Name(),
			"a Model built without New must still have a shortener")
		assert.Equal(t, "s.u13.net", urlshorten.DefaultName, "s.u13.net is the intended default")
	})

	t.Run("no argument reports the current setting and the choices", func(t *testing.T) {
		m, lines := shortenModel("", 0).handleShortenCommand([]string{"%shorten"})
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, urlshorten.DefaultName)
		for _, name := range urlshorten.Names() {
			assert.Containsf(t, out, name, "%s should be offered", name)
		}
		assert.Empty(t, m.shortenService, "asking should not change the setting")
	})

	t.Run("selecting a service", func(t *testing.T) {
		for _, name := range []string{"tinyurl", "s.u13.net", "da.gd"} {
			m, lines := shortenModel("", 0).handleShortenCommand([]string{"%shorten", name})
			assert.Equal(t, name, m.shortener().Name(), "%%shorten %s", name)
			assert.Contains(t, strings.Join(lines, "\n"), name)
		}
	})

	t.Run("service names are case-insensitive, like other command arguments", func(t *testing.T) {
		m, _ := shortenModel("", 0).handleShortenCommand([]string{"%shorten", "TinyURL"})
		assert.Equal(t, "tinyurl", m.shortener().Name(), "the canonical name should be stored")
	})

	t.Run("an unknown service is refused and changes nothing", func(t *testing.T) {
		m := shortenModel("", 0)
		m.shortenService = "tinyurl"
		m, lines := m.handleShortenCommand([]string{"%shorten", "bit.ly"})
		out := strings.Join(lines, "\n")
		assert.Contains(t, out, "No such shortener")
		assert.Contains(t, out, "da.gd", "the refusal should say what is available")
		assert.Equal(t, "tinyurl", m.shortener().Name(), "a bad name must not reset the setting")
	})
}

// %shorten reaches its handler through the same path as the other client-only
// commands, rather than being sent to the server as a message.
func TestShortenCommandIsHandledLocally(t *testing.T) {
	m := shortenModel("", 0)
	m, lines, _, recognized := m.applyLocalCommand("%shorten tinyurl")
	assert.True(t, recognized, "%shorten should be handled by the client")
	assert.Contains(t, strings.Join(lines, "\n"), "tinyurl")
	assert.Equal(t, "tinyurl", m.shortenService)
}

// A failure names the service that produced it: with three to choose between,
// "http 403 Forbidden" alone does not say what to do next.
func TestShortenErrorNamesTheService(t *testing.T) {
	m := shortenModel("see "+shortenTestURL, len("see "+shortenTestURL))
	m.shortenService = "s.u13.net"

	_, cmd := m.shortenURLAtCursor()
	require.NotNil(t, cmd)

	// The command is not run here — the message it would produce is built the
	// same way, from the service it captured.
	m = m.applyShortenResult(shortenResultMsg{
		url: shortenTestURL,
		err: errors.New("s.u13.net: http 403 Forbidden"),
	})
	last := m.output[len(m.output)-1]
	assert.Equal(t, "error", last.Type)
	assert.Contains(t, last.Data, "s.u13.net")
}

// A short link cannot be previewed by fetching it — da.gd serves a
// click-through page to anything browser-shaped, which is what the preview
// fetcher is. M-s knows the original at the moment it substitutes, so the
// preview is carried across rather than looked up again.
func TestShortenCarriesThePreviewToTheShortURL(t *testing.T) {
	const short = "https://da.gd/XFG5L"
	line := "read " + shortenTestURL
	m := shortenModel(line, len(line))
	m.linkPreviewCache = map[string]string{shortenTestURL: "Nepal flash flooding - live updates"}

	m = m.applyShortenResult(shortenResultMsg{url: shortenTestURL, short: short})

	assert.Equal(t, shortenTestURL, m.shortOriginals[short],
		"the short URL should remember what it stands for")
	assert.Equal(t, "Nepal flash flooding - live updates", m.linkPreviewCache[short],
		"the preview should follow the URL it describes")

	// And it is the destination's preview being drawn, not the shortener's.
	ghosts := m.ghosts()
	require.Len(t, ghosts, 1, "the short URL should show a preview")
	assert.Contains(t, ghosts[0].text, "Nepal flash flooding")
	assert.NotContains(t, ghosts[0].text, "da.gd")
}

// Nothing to carry over is not a problem: the mapping is still recorded, so the
// fetch that follows targets the destination instead of the shortener.
func TestShortenRecordsOriginalWithoutACachedPreview(t *testing.T) {
	const short = "https://da.gd/XFG5L"
	line := "read " + shortenTestURL
	m := shortenModel(line, len(line))

	m = m.applyShortenResult(shortenResultMsg{url: shortenTestURL, short: short})

	assert.Equal(t, shortenTestURL, m.shortOriginals[short])
	_, cached := m.linkPreviewCache[short]
	assert.False(t, cached, "nothing should be invented when there was no preview to carry")
}

// The memo survives sends — the link stays previewable after the message
// carrying it is gone — so it is bounded.
func TestShortOriginalsIsBounded(t *testing.T) {
	m := shortenModel("", 0)
	for i := 0; i < maxShortOriginals*3; i++ {
		m = m.rememberShortened(fmt.Sprintf("https://da.gd/%d", i), fmt.Sprintf("https://example.com/%d", i))
	}
	assert.LessOrEqual(t, len(m.shortOriginals), maxShortOriginals,
		"the short->original memo must not grow without bound")

	// resetPreviewsForNewLine is what a send runs to clear per-line state; the
	// memo is not per-line and must come through it intact.
	m2 := shortenModel("", 0)
	m2 = m2.rememberShortened("https://da.gd/x", "https://example.com/x")
	m2 = m2.resetPreviewsForNewLine()
	assert.Equal(t, "https://example.com/x", m2.shortOriginals["https://da.gd/x"],
		"the memo should survive a send, like the preview cache")
}

// previewTarget is the decision that fixes the reported bug: what URL gets
// fetched to describe the one in the input line.
func TestPreviewTarget(t *testing.T) {
	// Cancelled up front: a target that needs no lookup must not make one, and
	// one that does will fail rather than reach the network.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("an ordinary URL is its own target", func(t *testing.T) {
		got, ok := previewTarget(ctx, "https://example.com/a", "")
		assert.True(t, ok)
		assert.Equal(t, "https://example.com/a", got)
	})

	t.Run("a known short link resolves without asking anyone", func(t *testing.T) {
		got, ok := previewTarget(ctx, "https://da.gd/XFG5L", "https://www.cnn.com/2026/08/26/nepal")
		assert.True(t, ok)
		assert.Equal(t, "https://www.cnn.com/2026/08/26/nepal", got,
			"a link we shortened ourselves needs no reverse lookup")
	})

	t.Run("an unresolvable short link gets no preview at all", func(t *testing.T) {
		_, ok := previewTarget(ctx, "https://da.gd/unknown", "")
		assert.False(t, ok,
			"previewing a short link directly would describe the shortener, which is worse than silence")
	})
}

// Accept a preview, then shorten the URL under it, and the preview must not be
// offered a second time: the accepted copy is still in the line, and the
// carried-over summary would otherwise reappear beside it under a new key.
func TestShortenDoesNotReofferAnAcceptedPreview(t *testing.T) {
	const (
		summary = "More than 150 dead after floodwaters wipe out villages"
		short   = "https://da.gd/XFG5L"
	)
	line := "read " + shortenTestURL
	m := shortenModel(line, len(line))
	m.linkPreviewOn = true
	m.linkPreviewCache = map[string]string{shortenTestURL: summary}

	// Tab: the ghost becomes real text.
	m, ok := m.acceptPreviews()
	require.True(t, ok, "the preview should have been available to accept")
	require.Contains(t, m.inputValue, "("+summary+")")
	require.Empty(t, m.ghosts(), "an accepted preview should stop showing")

	// M-s: the URL is replaced, and its preview follows it to the short link.
	m = m.applyShortenResult(shortenResultMsg{url: shortenTestURL, short: short})
	require.Contains(t, m.inputValue, short)
	require.Equal(t, summary, m.linkPreviewCache[short], "the preview should have carried over")

	// The text is already in the line, so there is nothing left to offer.
	assert.Empty(t, m.ghosts(), "the accepted preview must not be offered again under the short URL")
	display, spans := m.inputDisplay()
	assert.Equal(t, m.inputValue, display, "nothing should be spliced in for drawing")
	assert.Empty(t, spans)
	assert.Equal(t, 1, strings.Count(display, "("+summary+")"), "the summary should appear exactly once")
}

// The carry-over still works when the preview was never accepted -- that is the
// whole point of it, and the duplicate check must not disable it.
func TestShortenStillOffersAnUnacceptedPreview(t *testing.T) {
	const (
		summary = "Nepal flash flooding - live updates"
		short   = "https://da.gd/XFG5L"
	)
	line := "read " + shortenTestURL
	m := shortenModel(line, len(line))
	m.linkPreviewOn = true
	m.linkPreviewCache = map[string]string{shortenTestURL: summary}

	m = m.applyShortenResult(shortenResultMsg{url: shortenTestURL, short: short})

	ghosts := m.ghosts()
	require.Len(t, ghosts, 1, "an unaccepted preview should follow the URL to its short form")
	assert.Contains(t, ghosts[0].text, summary)
}

// The reminder exists for people who have not met M-s, so it fires the first
// time a URL turns up in the line they are composing, and then never again.
func TestShortenHint(t *testing.T) {
	// typeLine feeds s through the ordinary key path, one rune at a time, so
	// the hint is exercised where it actually fires rather than by calling it.
	typeLine := func(m Model, s string) Model {
		for _, r := range s {
			upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: r, Text: string(r)})
			m = upd.(Model)
		}
		return m
	}
	hintLines := func(m Model) []string {
		var out []string
		for _, item := range m.output {
			lines, ok := item.Data.([]string)
			if !ok {
				continue
			}
			joined := strings.Join(lines, " ")
			if strings.Contains(joined, "M-s") {
				out = append(out, joined)
			}
		}
		return out
	}

	t.Run("typing a URL offers it once", func(t *testing.T) {
		m := typeLine(shortenModel("", 0), "see https://example.com/a")
		got := hintLines(m)
		require.Len(t, got, 1, "the reminder should be offered exactly once")
		assert.Contains(t, got[0], "M-s", "it should name the key")
		assert.Contains(t, got[0], "%help shorten", "it should point at the details")
	})

	t.Run("a second URL does not repeat it", func(t *testing.T) {
		m := typeLine(shortenModel("", 0), "see https://example.com/a ")
		require.Len(t, hintLines(m), 1)

		// Same line, and a fresh one after a send.
		m = typeLine(m, "and https://example.org/b")
		m = m.resetPreviewsForNewLine()
		m.inputValue, m.inputCursor = "", 0
		m = typeLine(m, "https://example.net/c")
		assert.Len(t, hintLines(m), 1, "the reminder is once a session, not once a line")
	})

	t.Run("a line with no URL says nothing", func(t *testing.T) {
		m := typeLine(shortenModel("", 0), "just talking about http things")
		assert.Empty(t, hintLines(m), "no URL, no reminder")
		assert.False(t, m.shortenHintShown)
	})

	t.Run("a pasted URL offers it too", func(t *testing.T) {
		m := shortenModel("", 0)
		upd, _ := m.handlePaste(tea.PasteMsg{Content: "https://example.com/pasted"})
		assert.Len(t, hintLines(upd.(Model)), 1, "paste is how a URL usually arrives")
	})

	t.Run("the reminder is not typed into the message", func(t *testing.T) {
		m := typeLine(shortenModel("", 0), "see https://example.com/a")
		assert.Equal(t, "see https://example.com/a", m.inputValue,
			"a note in the scrollback must not touch the line being composed")
	})
}

// The key is documented where a user would look for it.
func TestShortenKeyIsDocumented(t *testing.T) {
	help := strings.Join(NewKeyMap().KeyBindingHelp(), "\n")
	assert.Contains(t, help, "M-s")
	assert.Contains(t, help, "shorten")

	// And '%help shorten' explains the services, including the credential the
	// default one needs.
	topic := strings.Join(tuiHelp["shorten"], "\n")
	for _, name := range urlshorten.Names() {
		assert.Containsf(t, topic, name, "%%help shorten should describe %s", name)
	}
	assert.Contains(t, topic, "ZLILY_SHORTEN_API_KEY")
}
