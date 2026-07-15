package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMain pins osc8Enabled on so tests are deterministic regardless of the
// TERM the test environment happens to have (under screen it defaults off).
func TestMain(m *testing.M) {
	osc8Enabled = true
	os.Exit(m.Run())
}

// withOSC8 sets osc8Enabled for the duration of one test.
func withOSC8(t *testing.T, on bool) {
	t.Helper()
	old := osc8Enabled
	osc8Enabled = on
	t.Cleanup(func() { osc8Enabled = old })
}

func TestContainsURL(t *testing.T) {
	assert.True(t, containsURL("see http://example.com for more"))
	assert.True(t, containsURL("https://example.com/path?q=1"))
	assert.False(t, containsURL("no links here"))
	assert.False(t, containsURL("ftp://example.com not matched"))
}

func TestLinkifyText_WrapsURL(t *testing.T) {
	url := "https://example.com/path"
	out := linkifyText("visit " + url + " now")

	// The plain text around the URL is preserved.
	assert.Contains(t, out, "visit ")
	assert.Contains(t, out, " now")
	// The URL becomes an OSC8 hyperlink: ESC ] 8 ; ; <url> ... and the URL is
	// present as both the link target and the visible text.
	assert.Contains(t, out, "\x1b]8;;"+url)
	assert.Greater(t, strings.Count(out, url), 1, "URL should appear as both target and display text")
}

func TestLinkifyText_StripsTrailingPunctuation(t *testing.T) {
	// A trailing period should not be part of the link target.
	out := linkifyText("see https://example.com.")
	assert.Contains(t, out, "\x1b]8;;https://example.com\x1b")
	// The period is preserved outside the link, after the closing sequence.
	assert.True(t, strings.HasSuffix(out, "."), "trailing period kept outside the link: %q", out)
}

func TestLinkifyText_NoURLUnchanged(t *testing.T) {
	in := "just some plain text"
	assert.Equal(t, in, linkifyText(in))
}

// Under GNU screen (TERM=screen*) OSC8 must not be emitted at all: screen
// never forwards it, and old builds overflow their 256-byte escape buffer on
// a long URL and spill the tail onto the screen as literal text, corrupting
// the TUI layout (vanished status bar, stale -- MORE -- fragments).
func TestOSC8Disabled_PlainTextOnly(t *testing.T) {
	withOSC8(t, false)
	in := "visit https://example.com/path now"
	assert.Equal(t, in, linkifyText(in))
	assert.Equal(t, "frag", osc8Link("https://example.com/path", "frag", 1))
}

// A URL longer than maxOSC8URLLen is rendered as plain text even on capable
// terminals: emulators bound the escape sequences they will parse (iTerm2
// ignores hyperlinks over 2083 bytes), so the link would be unusable anyway.
func TestOSC8OverlongURL_PlainTextOnly(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("x", maxOSC8URLLen)
	assert.Equal(t, "frag", osc8Link(url, "frag", 1))
	in := "see " + url + " there"
	assert.Equal(t, in, linkifyText(in))
}
