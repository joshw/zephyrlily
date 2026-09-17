package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
)

// osc8Enabled gates all OSC8 hyperlink emission. GNU screen never forwards
// OSC8 to the outer terminal, so hyperlinks cannot work under it — and old
// builds (e.g. the 4.00.03 that macOS ships) overflow their 256-byte
// escape-string buffer on a long URL and print the overflow as literal text,
// wrapping physical lines and desyncing the renderer's idea of the screen
// (vanishing status bar, stale -- MORE -- fragments). Screen sets TERM=screen*.
var osc8Enabled = !strings.HasPrefix(os.Getenv("TERM"), "screen")

// maxOSC8URLLen caps the URL embedded in an OSC8 sequence. Terminals bound
// the sequences they will parse (iTerm2 ignores hyperlink URLs over 2083
// bytes) and multiplexers must buffer them to pass them through; a target
// this long is unusable anyway, so such URLs are shown as plain text.
const maxOSC8URLLen = 2000

// urlPattern matches common URL schemes (http and https).
// Matches URLs starting with http:// or https:// up to whitespace or certain
// delimiters. Parentheses are deliberately allowed: real URLs contain them
// (a Cell article id is "S0960-9822(26)01110-3", Wikipedia disambiguates with
// "_(band)"), and excluding them cut such a link in half everywhere the client
// finds URLs — most visibly in the input line, where the tail after "(" was
// left outside the span and the preview was fetched for the truncated URL.
// The parenthesised-aside case, "(see https://example.com/x)", is handled at
// the other end by trimURLEnd rather than by the pattern.
var urlPattern = regexp.MustCompile(`https?://[^\s<>\[\]]+`)

// trailingURLPunct is punctuation that should not be considered part of a URL
// when it appears at the very end of a matched span (e.g. a sentence-ending ".").
const trailingURLPunct = ".,;:!?\"'"

// trimURLEnd returns the end offset for the URL matched at s[start:end] with
// trailing sentence punctuation and unbalanced closing parentheses removed.
// A ")" is only cut when no "(" in the URL pairs with it, which is exactly the
// case of a URL written inside parentheses; a ")" that closes one belonging to
// the URL itself is kept.
func trimURLEnd(s string, start, end int) int {
	for end > start {
		switch {
		case strings.IndexByte(trailingURLPunct, s[end-1]) != -1:
			end--
		case s[end-1] == ')' && !parensBalanced(s[start:end]):
			end--
		default:
			return end
		}
	}
	return end
}

// parensBalanced reports whether every ")" in s is closed by an earlier "(".
// Unclosed "(" are fine — only a surplus ")" says the URL ran past its end.
func parensBalanced(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth < 0 {
				return false
			}
		}
	}
	return true
}

// linkID hands out monotonically increasing OSC8 hyperlink ids so that the
// fragments of a single URL split across wrapped lines share an id and are
// treated as one logical link by supporting terminals.
var linkID atomic.Int64

// osc8Link wraps text in an OSC8 hyperlink pointing at url, tagged with id so
// that multiple fragments of the same wrapped URL group together on hover.
// Terminals without OSC8 support ignore the escapes and show text plainly.
func osc8Link(url, text string, id int64) string {
	if !osc8Enabled || len(url) > maxOSC8URLLen {
		return text
	}
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
	start = loc[0]
	end = trimURLEnd(word, start, loc[1])
	return start, end, word[start:end]
}

// linkifyText replaces URLs in text with clickable hyperlinks using OSC8 sequences.
// This works in terminals that support hyperlinks (iTerm2, Kitty, Alacritty, Windows Terminal, etc.)
// and degrades gracefully to plain text in unsupported terminals.
func linkifyText(text string) string {
	locs := urlPattern.FindAllStringIndex(text, -1)
	if locs == nil {
		return text
	}
	var sb strings.Builder
	prev := 0
	for _, loc := range locs {
		start, matchEnd := loc[0], loc[1]
		// Trailing punctuation and asides' closing parens are shown but not linked.
		end := trimURLEnd(text, start, matchEnd)
		cleanURL := text[start:end]
		sb.WriteString(text[prev:start])
		if !osc8Enabled || end <= start || len(cleanURL) > maxOSC8URLLen {
			sb.WriteString(text[start:matchEnd])
		} else {
			// Link the URL text to itself. Ungrouped (id-less) form, matching the
			// termenv.Hyperlink output this replaces; osc8Link is the grouped form.
			fmt.Fprintf(&sb, "\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", cleanURL, cleanURL)
			sb.WriteString(text[end:matchEnd])
		}
		prev = matchEnd
	}
	sb.WriteString(text[prev:])
	return sb.String()
}

// containsURL returns true if text contains any URLs.
func containsURL(text string) bool {
	return urlPattern.MatchString(text)
}
