package ui

import (
	"regexp"
	"strings"

	"github.com/muesli/termenv"
)

// urlPattern matches common URL schemes (http and https).
// Matches URLs starting with http:// or https:// up to whitespace or certain delimiters.
var urlPattern = regexp.MustCompile(`https?://[^\s<>\[\]()]+`)

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
