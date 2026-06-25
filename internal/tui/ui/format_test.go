package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// osc8Re matches a single OSC8 introducer or terminator: ESC ] 8 ; ... ESC \.
var osc8Re = regexp.MustCompile("\x1b\\]8;[^\x1b]*\x1b\\\\")

// stripOSC8 removes OSC8 hyperlink escapes, leaving the visible text.
func stripOSC8(s string) string { return osc8Re.ReplaceAllString(s, "") }

// ansiRe matches SGR color escapes emitted by lipgloss styling.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripStyle removes both OSC8 and SGR escapes, leaving the visible text.
func stripStyle(s string) string { return ansiRe.ReplaceAllString(stripOSC8(s), "") }

func TestWrapText_Empty(t *testing.T) {
	// With no words, the current line is returned unchanged.
	got := wrapText("prefix> ", "  ", "", 40, " ")
	assert.Equal(t, []string{"prefix> "}, got)
}

func TestWrapText_SimpleWrap(t *testing.T) {
	// maxWidth small enough to force a wrap; continuation lines get wordPrefix.
	got := wrapText("", "    ", "one two three four", 10, "")
	require.NotEmpty(t, got)
	for _, line := range got {
		assert.LessOrEqual(t, len(line), 10, "line exceeds maxWidth: %q", line)
	}
	// Continuation lines start with the word prefix.
	if len(got) > 1 {
		assert.Equal(t, "    ", got[1][:4])
	}
}

func TestWrapText_HardBreaksLongWord(t *testing.T) {
	// A single token longer than maxWidth must be split across lines.
	long := "abcdefghijklmnopqrstuvwxyz"
	got := wrapText("", "", long, 10, "")
	require.Greater(t, len(got), 1, "long word should span multiple lines")
	var rejoined string
	for _, line := range got {
		assert.LessOrEqual(t, len(line), 10)
		rejoined += line
	}
	assert.Equal(t, long, rejoined, "no characters lost when hard-breaking")
}

func TestWrapText_InitialSep(t *testing.T) {
	// initialSep separates curLine from the first appended word.
	got := wrapText("you say", " ", "hello there", 40, ": ")
	require.NotEmpty(t, got)
	assert.Equal(t, "you say: hello there", got[0])
}

func TestWrapTextLinkify_NoURLMatchesPlain(t *testing.T) {
	// With no URLs, linkify output must be identical to plain wrapping.
	text := "the quick brown fox jumps over the lazy dog"
	plain := wrapText("", "  ", text, 12, "")
	linked := wrapTextLinkify("", "  ", text, 12, "")
	assert.Equal(t, plain, linked)
}

func TestWrapTextLinkify_InlineURL(t *testing.T) {
	url := "https://example.com/x"
	lines := wrapTextLinkify("", "", "visit "+url+" now", 80, "")
	require.Len(t, lines, 1)
	// Visible text is unchanged; the URL is wrapped in an OSC8 link to itself.
	assert.Equal(t, "visit "+url+" now", stripOSC8(lines[0]))
	assert.Contains(t, lines[0], ";"+url+"\x1b\\")
}

func TestWrapTextLinkify_StripsTrailingPunct(t *testing.T) {
	lines := wrapTextLinkify("", "", "see https://example.com.", 80, "")
	require.Len(t, lines, 1)
	// The link target excludes the trailing period.
	assert.Contains(t, lines[0], ";https://example.com\x1b\\")
	// The period survives as visible text.
	assert.True(t, strings.HasSuffix(stripOSC8(lines[0]), "."))
}

var idRe = regexp.MustCompile(`id=(\d+);`)

// assertSharedID checks that every OSC8 id in s is identical (one logical link).
func assertSharedID(t *testing.T, s string) {
	t.Helper()
	ids := idRe.FindAllStringSubmatch(s, -1)
	require.GreaterOrEqual(t, len(ids), 2, "expected multiple link fragments")
	for _, m := range ids {
		assert.Equal(t, ids[0][1], m[1], "wrapped URL fragments must share one id")
	}
}

func TestWrapTextLinkify_LongURLCharWraps(t *testing.T) {
	// A URL longer than the width is hard-wrapped at the width boundary so the
	// whole URL stays visible (for terminals without OSC8 support), never split
	// at a hyphen, with no prefix on continuation lines and all fragments sharing
	// one OSC8 id.
	url := "https://example.com/a/very/long/path/that/keeps/going-and-going"
	lines := wrapTextLinkify("", "", url, 20, "")
	require.Greater(t, len(lines), 1, "long URL must wrap across lines")

	joined := strings.Join(lines, "")
	for _, l := range lines {
		assert.LessOrEqual(t, len(stripOSC8(l)), 20, "visible line exceeds width: %q", stripOSC8(l))
	}
	// No characters lost: the full URL is reconstructed from the visible text.
	assert.Equal(t, url, stripOSC8(joined))
	assertSharedID(t, joined)
}

func TestWrapTextLinkify_URLContinuationNoPrefix(t *testing.T) {
	// Even with a word prefix, the continuation lines of a wrapped URL carry no
	// prefix so the URL reads back intact across lines.
	url := "https://example.com/a/very/long/path/that/keeps/going-and-going"
	lines := wrapTextLinkify(" - ", " - ", url, 20, "")
	require.Greater(t, len(lines), 1)
	for i, l := range lines {
		if i == 0 {
			continue
		}
		assert.False(t, strings.HasPrefix(stripOSC8(l), " - "),
			"continuation line %d must not carry the word prefix: %q", i, stripOSC8(l))
	}
}

func TestCharWrapLinkify_LongURL(t *testing.T) {
	// The command-output path char-wraps a long URL at the width boundary while
	// keeping it one clickable OSC8 link with the full URL visible.
	url := "https://docs.google.com/document/d/1cNchNxdLELxxw21XbUFAsem-5skN6SxqBaCTWrh9XFI/edit?usp=sharing"
	lines := charWrapLinkify(url, 40)
	require.Greater(t, len(lines), 1, "long URL must wrap across lines")

	joined := strings.Join(lines, "")
	for _, l := range lines {
		assert.LessOrEqual(t, len(stripOSC8(l)), 40)
	}
	assert.Equal(t, url, stripOSC8(joined))
	assert.Contains(t, joined, ";"+url+"\x1b\\", "fragments link to the full URL")
	assertSharedID(t, joined)
}

func TestCharWrapLinkify_ShortLineUnchanged(t *testing.T) {
	// A line within the width is returned as a single line, URLs linked in place.
	lines := charWrapLinkify("see https://example.com now", 80)
	require.Len(t, lines, 1)
	assert.Equal(t, "see https://example.com now", stripOSC8(lines[0]))
	assert.Contains(t, lines[0], ";https://example.com\x1b\\")
}

func TestFormatEvent_StampInsertsTimestampIntoBanner(t *testing.T) {
	const epoch = int64(1700000000)
	// Timestamp is rendered in local time, so derive the expected value the same
	// way formatEvent does rather than hard-coding a zone-specific string.
	lt := time.Unix(epoch, 0)
	wantTS := fmt.Sprintf("(%02d:%02d) ", lt.Hour(), lt.Minute())

	d := map[string]interface{}{
		"event": "connect",
		"text":  "*** Alice has entered lily ***",
		"stamp": true,
		"time":  float64(epoch),
	}
	got := stripStyle(formatEvent(d, 80, "me"))
	assert.Equal(t, "*** "+wantTS+"Alice has entered lily ***", got)

	// Without STAMP the banner is left untouched.
	d["stamp"] = false
	got = stripStyle(formatEvent(d, 80, "me"))
	assert.Equal(t, "*** Alice has entered lily ***", got)
}

func TestFormatEvent_StampLeavesQuietSelfNoticeAlone(t *testing.T) {
	// Quiet "(…)" self-notices carry no %T in tigerlily; we mirror that.
	d := map[string]interface{}{
		"event": "rename",
		"text":  "(you are now named Alicia)",
		"stamp": true,
		"time":  float64(1700000000),
	}
	got := stripStyle(formatEvent(d, 80, "me"))
	assert.Equal(t, "(you are now named Alicia)", got)
}
