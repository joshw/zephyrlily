package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
