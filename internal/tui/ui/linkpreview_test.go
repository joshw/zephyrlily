package ui

import (
	"strings"
	"testing"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/joshw/zephyrlily/internal/linkpreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// previewModel builds a model with previews already resolved, as if the fetches
// had come back, so the tests exercise the display and editing behaviour rather
// than the network.
func previewModel(value string, cursor int, cache map[string]string) Model {
	return Model{
		keys:             NewKeyMap(),
		input:            textarea.New(),
		width:            80,
		height:           24,
		spellChecker:     NewSpellChecker(),
		inputValue:       value,
		inputCursor:      cursor,
		linkPreviewOn:    true,
		linkPreviewCache: cache,
	}
}

// A preview is an offer of text. If the line already carries that text, there
// is nothing to offer, whichever URL it happens to be sitting under.
func TestPreviewNotOfferedWhenTextIsAlreadyPresent(t *testing.T) {
	const summary = "Some Article Title"

	t.Run("present after the url", func(t *testing.T) {
		m := previewModel("https://a.co ("+summary+")", 0, map[string]string{"https://a.co": summary})
		assert.Empty(t, m.ghosts())
	})

	t.Run("present further along the line", func(t *testing.T) {
		// Where shortening leaves it: the bracketed host sits in between.
		m := previewModel("https://a.co [example.com] ("+summary+")", 0,
			map[string]string{"https://a.co": summary})
		assert.Empty(t, m.ghosts())
	})

	t.Run("present with edited spacing", func(t *testing.T) {
		m := previewModel("https://a.co("+summary+")", 0, map[string]string{"https://a.co": summary})
		assert.Empty(t, m.ghosts())
	})

	t.Run("absent, so still offered", func(t *testing.T) {
		m := previewModel("https://a.co (Something Else)", 0, map[string]string{"https://a.co": summary})
		require.Len(t, m.ghosts(), 1)
		assert.Contains(t, m.ghosts()[0].text, summary)
	})

	t.Run("a placeholder is not repeated either", func(t *testing.T) {
		m := previewModel("https://a.co ("+noPreviewText+")", 0, map[string]string{"https://a.co": ""})
		assert.Empty(t, m.ghosts(), "the placeholder text is already in the line")
	})
}

func TestInputURLSpans(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []urlSpan
	}{
		{"none", "no links here", nil},
		{"bare", "https://a.co", []urlSpan{{0, 12, "https://a.co", 0}}},
		{
			"trailing punctuation excluded",
			"see https://a.co.",
			[]urlSpan{{4, 16, "https://a.co", 0}},
		},
		{
			"two distinct urls",
			"https://a.co and http://b.co",
			[]urlSpan{{0, 12, "https://a.co", 0}, {17, 28, "http://b.co", 0}},
		},
		{
			"same url twice gets occurrence indices",
			"https://a.co https://a.co",
			[]urlSpan{{0, 12, "https://a.co", 0}, {13, 25, "https://a.co", 1}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inputURLSpans(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d spans %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("span %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// The display string weaves previews in; inputValue must stay exactly what the
// user typed, since that is what gets sent.
func TestInputDisplayLeavesInputValueAlone(t *testing.T) {
	m := previewModel("see https://a.co ok", 0, map[string]string{"https://a.co": "A Site"})

	display, spans := m.inputDisplay()
	if want := "see https://a.co (A Site) ok"; display != want {
		t.Errorf("display = %q, want %q", display, want)
	}
	if m.inputValue != "see https://a.co ok" {
		t.Errorf("inputValue changed to %q", m.inputValue)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d ghost spans, want 1", len(spans))
	}
	if got := display[spans[0].start:spans[0].end]; got != " (A Site)" {
		t.Errorf("ghost span covers %q, want %q", got, " (A Site)")
	}
}

// A URL that resolved to nothing says so, rather than drawing an empty "()" or
// staying silent in a way that looks like the fetch never happened.
func TestEmptySummaryDrawsPlaceholder(t *testing.T) {
	m := previewModel("https://a.co", 12, map[string]string{"https://a.co": ""})

	display, spans := m.inputDisplay()
	if want := "https://a.co (no preview available)"; display != want {
		t.Errorf("display = %q, want %q", display, want)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d ghost spans, want 1", len(spans))
	}
	gs := m.ghosts()
	if len(gs) != 1 || !gs[0].placeholder {
		t.Errorf("expected one placeholder ghost, got %+v", gs)
	}
}

// A URL not yet fetched — or still in flight — draws nothing at all. The
// placeholder claims the lookup finished, so it must not appear before it has.
func TestUnfetchedURLDrawsNothing(t *testing.T) {
	t.Run("never asked", func(t *testing.T) {
		m := previewModel("https://a.co", 12, map[string]string{})
		if gs := m.ghosts(); len(gs) != 0 {
			t.Errorf("drew %d ghosts for a URL never fetched: %+v", len(gs), gs)
		}
	})
	t.Run("in flight", func(t *testing.T) {
		m := previewModel("https://a.co", 12, map[string]string{})
		m.linkPreviewPending = map[string]bool{"https://a.co": true}
		if gs := m.ghosts(); len(gs) != 0 {
			t.Errorf("drew %d ghosts while the fetch was in flight", len(gs))
		}
	})
}

// The placeholder must have no path into inputValue, since inputValue is what
// gets sent.
func TestPlaceholderCannotBeAccepted(t *testing.T) {
	const line = "https://a.co"
	m := previewModel(line, len(line), map[string]string{"https://a.co": ""})

	upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyTab})
	got := upd.(Model)
	if got.inputValue != line {
		t.Errorf("Tab put placeholder text into the input: %q", got.inputValue)
	}
	if strings.Contains(got.inputValue, noPreviewText) {
		t.Errorf("placeholder leaked into the outgoing line: %q", got.inputValue)
	}
	// Still showing, because nothing about it changed.
	if gs := got.ghosts(); len(gs) != 1 || !gs[0].placeholder {
		t.Errorf("placeholder should survive Tab, got %+v", gs)
	}
}

// With only a placeholder on screen Tab has nothing to accept, so completion
// must still get its turn rather than Tab going dead.
func TestTabReachesCompletionPastAPlaceholder(t *testing.T) {
	m := previewModel("https://a.co", 12, map[string]string{"https://a.co": ""})
	if _, ok := m.acceptPreviews(); ok {
		t.Errorf("acceptPreviews claimed a placeholder as accepted")
	}
}

// A line carrying both kinds: Tab takes the real one and steps over the other.
func TestAcceptTakesRealPreviewsOnly(t *testing.T) {
	const line = "https://a.co and https://b.co"
	m := previewModel(line, len(line), map[string]string{
		"https://a.co": "Site A",
		"https://b.co": "",
	})

	upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyTab})
	got := upd.(Model)

	want := "https://a.co (Site A) and https://b.co"
	if got.inputValue != want {
		t.Errorf("after Tab inputValue = %q, want %q", got.inputValue, want)
	}
	gs := got.ghosts()
	if len(gs) != 1 || !gs[0].placeholder {
		t.Errorf("expected only the placeholder left showing, got %+v", gs)
	}
}

func TestPreviewCoordinateRoundTrip(t *testing.T) {
	m := previewModel("a https://a.co b", 0, map[string]string{"https://a.co": "S"})
	gs := m.ghosts()

	// Every input offset maps into display coordinates and back unchanged.
	for off := 0; off <= len(m.inputValue); off++ {
		if got := toInput(gs, toDisplay(gs, off)); got != off {
			t.Errorf("round trip of input offset %d gave %d", off, got)
		}
	}

	// An offset inside the ghost collapses to the end of the URL it describes.
	ghostStart := toDisplay(gs, 14) // just past "a https://a.co"
	if got := toInput(gs, ghostStart+2); got != 14 {
		t.Errorf("offset inside ghost mapped to %d, want 14 (end of URL)", got)
	}
}

// Tab turns every showing preview into real text, and only then does it reach
// inputValue — which is what the send path reads.
func TestTabAcceptsAllPreviews(t *testing.T) {
	m := previewModel(
		"https://a.co and https://b.co",
		len("https://a.co and https://b.co"),
		map[string]string{"https://a.co": "Site A", "https://b.co": "Site B"},
	)

	upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyTab})
	got := upd.(Model)

	want := "https://a.co (Site A) and https://b.co (Site B)"
	if got.inputValue != want {
		t.Errorf("after Tab inputValue = %q, want %q", got.inputValue, want)
	}
	if got.inputCursor != len(want) {
		t.Errorf("cursor = %d, want %d (end of accepted text)", got.inputCursor, len(want))
	}
	// Accepted previews must not then be offered a second time on the text
	// they were just spliced into.
	if gs := got.ghosts(); len(gs) != 0 {
		t.Errorf("still showing %d previews after accepting", len(gs))
	}
}

// With no preview showing, Tab must still reach completion.
func TestTabFallsThroughToCompletionWithoutPreviews(t *testing.T) {
	m := previewModel("hello", 5, nil)
	upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := upd.(Model); got.inputValue != "hello" {
		t.Errorf("Tab with no previews changed input to %q", got.inputValue)
	}
}

// Backspace at the end of a previewed URL takes the preview, not a character.
func TestBackspaceDismissesPreviewThenEdits(t *testing.T) {
	const line = "https://a.co"
	m := previewModel(line, len(line), map[string]string{"https://a.co": "Site A"})

	upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	first := upd.(Model)
	if first.inputValue != line {
		t.Fatalf("first Backspace edited the text: %q", first.inputValue)
	}
	if gs := first.ghosts(); len(gs) != 0 {
		t.Fatalf("first Backspace left %d previews showing", len(gs))
	}

	// The second one edits, now that there is no preview to take.
	upd, _ = first.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := upd.(Model); got.inputValue != "https://a.c" {
		t.Errorf("second Backspace gave %q, want %q", got.inputValue, "https://a.c")
	}
}

// Dismissing one preview must leave the others alone — including when the same
// URL appears twice, where only the occurrence at the cursor goes.
func TestBackspaceDismissesOnlyTheCursorsPreview(t *testing.T) {
	t.Run("distinct urls", func(t *testing.T) {
		const line = "https://a.co and https://b.co"
		m := previewModel(line, len("https://a.co"),
			map[string]string{"https://a.co": "Site A", "https://b.co": "Site B"})

		upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
		gs := upd.(Model).ghosts()
		if len(gs) != 1 {
			t.Fatalf("got %d previews left, want 1", len(gs))
		}
		if gs[0].text != " (Site B)" {
			t.Errorf("wrong preview survived: %q", gs[0].text)
		}
	})

	t.Run("same url twice", func(t *testing.T) {
		const line = "https://a.co https://a.co"
		m := previewModel(line, len(line), map[string]string{"https://a.co": "Site A"})

		upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
		gs := upd.(Model).ghosts()
		if len(gs) != 1 {
			t.Fatalf("got %d previews left, want 1", len(gs))
		}
		if gs[0].at != len("https://a.co") {
			t.Errorf("surviving preview anchored at %d, want the first occurrence at %d",
				gs[0].at, len("https://a.co"))
		}
	})
}

// The whole point: a preview the user did not accept never reaches the message.
func TestUnacceptedPreviewIsNotSent(t *testing.T) {
	const line = "look at https://a.co"
	m := previewModel(line, len(line), map[string]string{"https://a.co": "Site A"})
	m.client = nil // submitLine only echoes when there is no client to send on

	if gs := m.ghosts(); len(gs) != 1 {
		t.Fatalf("expected a preview to be showing, got %d", len(gs))
	}

	// The preview is on screen…
	display, _ := m.inputDisplay()
	if !strings.Contains(display, "(Site A)") {
		t.Fatalf("preview not drawn: %q", display)
	}
	// …but what the send path reads is inputValue, which never saw it.
	if m.inputValue != line {
		t.Fatalf("inputValue = %q, want %q", m.inputValue, line)
	}
	if strings.Contains(m.inputValue, "Site A") {
		t.Fatalf("preview text leaked into the outgoing line: %q", m.inputValue)
	}
}

// Editing the URL retracts its preview, because the text no longer matches what
// was fetched.
func TestEditingURLRetractsPreview(t *testing.T) {
	m := previewModel("https://a.co", 12, map[string]string{"https://a.co": "Site A"})
	m.inputValue = "https://a.coX"
	if gs := m.ghosts(); len(gs) != 0 {
		t.Errorf("preview survived an edit to the URL: %+v", gs)
	}
}

func TestPreviewsOffShowsNothing(t *testing.T) {
	m := previewModel("https://a.co", 12, map[string]string{"https://a.co": "Site A"})
	m.linkPreviewOn = false
	if gs := m.ghosts(); len(gs) != 0 {
		t.Errorf("previews shown while switched off: %+v", gs)
	}
	if _, cmds := m.previewCmds(true); cmds != nil {
		t.Errorf("fetches started while switched off")
	}
}

// Typing must not fetch a URL that is still being typed; only one with
// something after it, or an outright paste, is fetched.
func TestFetchTriggerPolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		force bool
		want  int
	}{
		{"still being typed", "https://a.c", false, 0},
		{"followed by a space", "https://a.co ", false, 1},
		{"pasted while incomplete-looking", "https://a.c", true, 1},
		{"two urls after a delimiter", "https://a.co https://b.co!", false, 2},
		{"no url at all", "just text", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := previewModel(tc.value, len(tc.value), map[string]string{})
			m.linkPreviewPending = map[string]bool{}
			_, cmds := m.previewCmds(tc.force)
			if len(cmds) != tc.want {
				t.Errorf("started %d fetches, want %d", len(cmds), tc.want)
			}
		})
	}
}

// A URL already fetched, or already in flight, must not be fetched again on the
// next keystroke.
func TestFetchNotRepeated(t *testing.T) {
	t.Run("already cached", func(t *testing.T) {
		m := previewModel("https://a.co ", 13, map[string]string{"https://a.co": "Site A"})
		if _, cmds := m.previewCmds(false); cmds != nil {
			t.Errorf("refetched a URL already in the cache")
		}
	})
	t.Run("empty result still counts as fetched", func(t *testing.T) {
		m := previewModel("https://a.co ", 13, map[string]string{"https://a.co": ""})
		if _, cmds := m.previewCmds(false); cmds != nil {
			t.Errorf("refetched a URL known to have no metadata")
		}
	})
	t.Run("in flight", func(t *testing.T) {
		m := previewModel("https://a.co ", 13, map[string]string{})
		m.linkPreviewPending = map[string]bool{"https://a.co": true}
		if _, cmds := m.previewCmds(false); cmds != nil {
			t.Errorf("started a second fetch for a URL already in flight")
		}
	})
}

// The input area must grow to fit preview text, or the ghost would be drawn
// over the status bar.
func TestPreviewExtendsInputGeometry(t *testing.T) {
	long := strings.Repeat("x", 90)
	m := previewModel("https://a.co", 12, map[string]string{"https://a.co": long})
	m.width = 40

	if got, want := m.inputDisplayLen(), len("https://a.co")+len(long)+3; got != want {
		t.Errorf("display length = %d, want %d", got, want)
	}
	withPreview := m.inputTotalLines()
	m.linkPreviewOn = false
	if without := m.inputTotalLines(); withPreview <= without {
		t.Errorf("input lines with preview (%d) should exceed without (%d)", withPreview, without)
	}
}

// Rendering must style the ghost text and leave the typed text unstyled by it.
func TestRenderStylesPreviewText(t *testing.T) {
	m := previewModel("https://a.co", 0, map[string]string{"https://a.co": "Site A"})
	out := m.renderInputArea()

	if !strings.Contains(ansi.Strip(out), "https://a.co (Site A)") {
		t.Errorf("rendered input missing the preview: %q", ansi.Strip(out))
	}
	// The gray SGR the preview style emits must appear somewhere in the output.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("rendered input carries no styling at all: %q", out)
	}
}

// Preview text is styled as whole runs, not character by character. A sentence
// of ghost text redrawn on every keystroke would otherwise put a kilobyte of
// escapes on the wire each time, and long escape bursts are what overruns
// screen's escape buffer.
func TestPreviewStylingIsCoalesced(t *testing.T) {
	summary := strings.Repeat("word ", 18) // ~90 chars, wider than one line
	m := previewModel("https://a.co", 0, map[string]string{"https://a.co": summary})
	m.width = 60

	out := m.renderInputArea()
	// One styled run per display line the preview covers, not one per rune.
	if n := strings.Count(out, "\x1b[90m"); n > 4 {
		t.Errorf("preview emitted %d style escapes; expected one per wrapped line", n)
	}
	if !strings.Contains(ansi.Strip(out), "word word") {
		t.Errorf("coalescing dropped preview text: %q", ansi.Strip(out))
	}
}

// A summary that only names the site the URL already names is not worth the
// screen space, and is dropped at fetch time so it is never drawn or refetched.
func TestEchoesHost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary string
		url     string
		want    bool
	}{
		// Suppressed: the summary reduces to part of the hostname.
		{"bare site name", "Reddit", "https://www.reddit.com/r/golang/", true},
		{"case and spacing folded", "git hub", "https://github.com/golang/go", true},
		{"host spelled out", "example.com", "https://example.com/a", true},
		{"short name inside host", "Go", "https://go.dev/blog", true},
		{"www stripped before compare", "BBC", "https://www.bbc.com/news", true},
		{"subdomain still counts", "Wikipedia", "https://en.wikipedia.org/wiki/X", true},

		// Kept: these say something the URL does not.
		{"real description", "Welcome to Cloudflare - Powering the next generation of applications",
			"https://www.cloudflare.com/", false},
		{"title beyond the site name", "Open Graph protocol - Wikipedia",
			"https://en.wikipedia.org/wiki/Open_Graph_protocol", false},
		{"site name not in host", "Hacker News", "https://news.ycombinator.com/", false},
		{"content type summary", "PDF: 1706.03762", "https://arxiv.org/pdf/1706.03762", false},
		{"empty summary", "", "https://reddit.com/", false},
		{"unparseable url", "Reddit", "://///", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := echoesHost(tc.summary, tc.url); got != tc.want {
				t.Errorf("echoesHost(%q, %q) = %v, want %v", tc.summary, tc.url, got, tc.want)
			}
		})
	}
}

// A suppressed summary caches as "" like any other empty result, so it neither
// draws nor triggers a refetch on the next keystroke.
func TestSuppressedSummaryBehavesLikeNoMetadata(t *testing.T) {
	m := previewModel("https://reddit.com ", len("https://reddit.com "), map[string]string{})
	m = m.applyPreviewResult(linkPreviewResultMsg{url: "https://reddit.com", summary: ""})

	gs := m.ghosts()
	if len(gs) != 1 || !gs[0].placeholder {
		t.Errorf("suppressed summary should show the placeholder, got %+v", gs)
	}
	if _, cmds := m.previewCmds(false); cmds != nil {
		t.Errorf("suppressed summary triggered a refetch")
	}
	if _, ok := m.acceptPreviews(); ok {
		t.Errorf("suppressed summary was acceptable")
	}
}

// Preview text must reach the input line as pure ASCII: Lily's wire strips
// anything else, and the renderer's byte-per-column wrap assumes it.
func TestPreviewTextIsASCII(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"truncation ellipsis", "cut here…", "cut here..."},
		{"curly quotes", "\u2018My Guardian\u2019: a story", "'My Guardian': a story"},
		{"apostrophe and em dash", "Trump\u2019s aide \u2014 loyalty", "Trump's aide -- loyalty"},
		{"accents folded", "caf\u00e9 na\u00efve", "cafe naive"},
		{"plain ascii untouched", "already plain", "already plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := previewText(tc.in)
			if got != tc.want {
				t.Errorf("previewText(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := 0; i < len(got); i++ {
				if got[i] > unicode.MaxASCII {
					t.Fatalf("non-ASCII byte %#x at %d in %q", got[i], i, got)
				}
			}
		})
	}
}

// Conversion can lengthen the text, so the cap is re-applied afterwards — a
// preview must not grow past its budget just because it was full of emoji.
func TestPreviewTextReappliesCap(t *testing.T) {
	// Scaled off the cap, not a fixed count, so this keeps overshooting however
	// the cap is retuned.
	got := previewText(strings.Repeat("\U0001F4DA", maxPreviewLen)) // each becomes "[BOOKS]"
	if len(got) > maxPreviewLen {
		t.Errorf("previewText returned %d chars, cap is %d", len(got), maxPreviewLen)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated text should end in an ASCII ellipsis, got %q", got)
	}
}

// The ghost text actually drawn, and the text Tab splices in, must both be
// ASCII end to end.
func TestAcceptedPreviewIsASCII(t *testing.T) {
	const line = "https://a.co"
	m := previewModel(line, len(line), map[string]string{
		"https://a.co": previewText("Trump\u2019s aide \u2014 \u2018loyalty\u2019…"),
	})

	display, _ := m.inputDisplay()
	upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyTab})
	accepted := upd.(Model).inputValue

	for _, s := range []string{display, accepted} {
		for i := 0; i < len(s); i++ {
			if s[i] > unicode.MaxASCII {
				t.Fatalf("non-ASCII byte %#x at %d in %q", s[i], i, s)
			}
		}
	}
	if !strings.Contains(accepted, "Trump's aide -- 'loyalty'...") {
		t.Errorf("accepted text = %q", accepted)
	}
}

// A headline that only names the site falls through to the description rather
// than suppressing the preview outright — the chain leads with og:title, which
// on a site root is the masthead, not a headline.
func TestSummaryFallsThroughAnEchoingHeadline(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    linkpreview.Preview
		want string
	}{{
		name: "headline echoes the host, description saves it",
		p: linkpreview.Preview{
			URL: "https://arstechnica.com/", Field: linkpreview.FieldOGTitle,
			Title: "Ars Technica", Desc: "News and reviews, covering IT and science.",
		},
		want: "News and reviews, covering IT and science.",
	}, {
		name: "headline is fine, description never consulted",
		p: linkpreview.Preview{
			URL: "https://arstechnica.com/x", Field: linkpreview.FieldOGTitle,
			Title: "A real headline", Desc: "A description.",
		},
		want: "A real headline",
	}, {
		name: "both echo, nothing to show",
		p: linkpreview.Preview{
			URL: "https://reddit.com/", Field: linkpreview.FieldOGTitle,
			Title: "Reddit", Desc: "reddit",
		},
		want: "",
	}, {
		name: "no description to fall through to",
		p: linkpreview.Preview{
			URL: "https://reddit.com/", Field: linkpreview.FieldOGTitle, Title: "Reddit",
		},
		want: "",
	}, {
		// The fall-through is headline-specific: a description that echoes has
		// nothing better below it in the chain.
		name: "echoing description is not retried",
		p: linkpreview.Preview{
			URL: "https://reddit.com/", Field: linkpreview.FieldOGDescription,
			Title: "A title", Desc: "Reddit",
		},
		want: "",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := summaryFor(tc.p, tc.p.URL); got != tc.want {
				t.Errorf("summaryFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLinkPreviewCommand(t *testing.T) {
	m := previewModel("", 0, nil)

	m, out := m.handleLinkPreviewCommand([]string{"%linkpreview"})
	if len(out) != 1 || !strings.Contains(out[0], "on") {
		t.Errorf("status with no argument = %v, want it to report on", out)
	}

	m, out = m.handleLinkPreviewCommand([]string{"%linkpreview", "off"})
	if m.linkPreviewOn {
		t.Errorf("%%linkpreview off left previews enabled")
	}
	if len(out) != 1 || !strings.Contains(out[0], "off") {
		t.Errorf("off confirmation = %v", out)
	}

	if _, out = m.handleLinkPreviewCommand([]string{"%linkpreview", "sideways"}); !strings.HasPrefix(out[0], "Usage:") {
		t.Errorf("bad argument gave %v, want usage", out)
	}
}

// Sending clears the dismissals so the next line starts fresh, but keeps the
// url→summary memo so re-pasting a link does not refetch it.
func TestResetPreviewsForNewLine(t *testing.T) {
	m := previewModel("https://a.co", 12, map[string]string{"https://a.co": "Site A"})
	m.linkPreviewDismissed = map[previewKey]bool{{url: "https://a.co"}: true}

	m = m.resetPreviewsForNewLine()
	if len(m.linkPreviewDismissed) != 0 {
		t.Errorf("dismissals survived a send: %v", m.linkPreviewDismissed)
	}
	if m.linkPreviewCache["https://a.co"] != "Site A" {
		t.Errorf("summary memo was dropped on send")
	}
}

func TestPreviewCacheIsBounded(t *testing.T) {
	m := previewModel("", 0, map[string]string{})
	for i := 0; i < maxPreviewCache*3; i++ {
		m = m.applyPreviewResult(linkPreviewResultMsg{
			url:     "https://a.co/" + strings.Repeat("x", i%50) + string(rune('a'+i%26)),
			summary: "s",
		})
	}
	if len(m.linkPreviewCache) > maxPreviewCache {
		t.Errorf("cache grew to %d entries, cap is %d", len(m.linkPreviewCache), maxPreviewCache)
	}
}
