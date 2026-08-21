// Package cmdarg provides case-insensitive matching for the closed-set keyword
// arguments of %commands, so that "%mouse ON", "%MOUSE on" and "%mouse on" all
// behave identically.
//
// Only tokens drawn from a fixed vocabulary belong here: command names,
// subcommands ("list", "clear"), and toggles ("on", "off"). Free-form arguments
// must never be passed through these helpers — their case is significant. In
// particular a %on "like" regexp changes meaning when lowercased (\S, \W and \B
// invert into \s, \w and \b), and message text, alias expansions, file paths and
// Lily memo names are all echoed or transmitted verbatim.
package cmdarg

import "strings"

// Fold returns tok lowercased, for comparing against a closed set of keywords or
// for use as a key in a map whose keys are lowercase.
func Fold(tok string) string {
	return strings.ToLower(tok)
}

// Is reports whether tok equals want, ignoring case.
func Is(tok, want string) bool {
	return strings.EqualFold(tok, want)
}

// Any reports whether tok equals any of want, ignoring case.
func Any(tok string, want ...string) bool {
	for _, w := range want {
		if strings.EqualFold(tok, w) {
			return true
		}
	}
	return false
}

// OnOff parses an on/off toggle argument. It accepts only "on" and "off" in any
// case; ok is false for anything else so callers can print their usage line.
func OnOff(tok string) (on bool, ok bool) {
	switch {
	case strings.EqualFold(tok, "on"):
		return true, true
	case strings.EqualFold(tok, "off"):
		return false, true
	}
	return false, false
}
