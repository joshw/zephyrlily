package ui

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/muesli/termenv"
)

// urlPattern matches common URL schemes (http and https).
// Matches URLs starting with http:// or https:// up to whitespace or certain delimiters.
var urlPattern = regexp.MustCompile(`https?://[^\s<>\[\]()]+`)

// trailingURLPunct is punctuation that should not be considered part of a URL
// when it appears at the very end of a matched span (e.g. a sentence-ending ".").
const trailingURLPunct = ".,;:!?\"'"

// linkID hands out monotonically increasing OSC8 hyperlink ids so that the
// fragments of a single URL split across wrapped lines share an id and are
// treated as one logical link by supporting terminals.
var linkID atomic.Int64

// osc8Link wraps text in an OSC8 hyperlink pointing at url, tagged with id so
// that multiple fragments of the same wrapped URL group together on hover.
// Terminals without OSC8 support ignore the escapes and show text plainly.
func osc8Link(url, text string, id int64) string {
	return fmt.Sprintf("\x1b]8;id=%d;%s\x1b\\%s\x1b]8;;\x1b\\", id, url, text)
}

// urlSpanInWord locates the first URL inside a single whitespace-delimited word
// and returns its byte span [start, end) with trailing punctuation excluded,
// plus the cleaned URL target. start is -1 when the word contains no URL.
func urlSpanInWord(word string) (start, end int, clean string) {
	loc := urlPattern.FindStringIndex(word)
	if loc == nil {
		return -1, -1, ""
	}
	start, end = loc[0], loc[1]
	for end > start && strings.IndexByte(trailingURLPunct, word[end-1]) != -1 {
		end--
	}
	return start, end, word[start:end]
}

// linkifyText replaces URLs in text with clickable hyperlinks using OSC8 sequences.
// This works in terminals that support hyperlinks (iTerm2, Kitty, Alacritty, Windows Terminal, etc.)
// and degrades gracefully to plain text in unsupported terminals.
func linkifyText(text string) string {
	return urlPattern.ReplaceAllStringFunc(text, func(url string) string {
		// Strip trailing punctuation that might have been captured
		cleanURL := strings.TrimRight(url, ".,;:!?\"'")
		// Return the hyperlink with the URL as both link target and display text
		return termenv.Hyperlink(cleanURL, cleanURL) + url[len(cleanURL):]
	})
}

// containsURL returns true if text contains any URLs.
func containsURL(text string) bool {
	return urlPattern.MatchString(text)
}
