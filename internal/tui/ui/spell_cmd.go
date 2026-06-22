package ui

import (
	"fmt"
	"strings"
)

// spellUsage is the one-line usage string shown for malformed %spell commands.
var spellUsage = []string{
	"Usage: %spell [list] | %spell on|off | %spell allow|forbid|remove <word>... | %spell reset",
}

// HandleCommand implements the %spell command. It mutates the receiver in place;
// the caller re-renders the input area so highlighting updates immediately.
// Returns the lines to display.
func (s *SpellChecker) HandleCommand(args []string) []string {
	if len(args) == 0 || args[0] == "list" {
		return s.statusLines()
	}

	switch args[0] {
	case "on":
		s.SetEnabled(true)
		return []string{"Spell checking on."}
	case "off":
		s.SetEnabled(false)
		return []string{"Spell checking off."}
	case "reset":
		s.ResetOverlays()
		return append([]string{"Spell overlays reset to defaults.", ""}, s.statusLines()...)
	case "allow", "forbid", "remove":
		words := args[1:]
		if len(words) == 0 {
			return spellUsage
		}
		return s.applyWords(args[0], words)
	}

	return spellUsage
}

// applyWords runs allow/forbid/remove over each word and returns a summary.
func (s *SpellChecker) applyWords(action string, words []string) []string {
	var out []string
	for _, w := range words {
		switch action {
		case "allow":
			s.Allow(w)
			out = append(out, fmt.Sprintf("Allowed %q.", w))
		case "forbid":
			s.Forbid(w)
			out = append(out, fmt.Sprintf("Forbade %q.", w))
		case "remove":
			if s.Remove(w) {
				out = append(out, fmt.Sprintf("Removed %q from overlays.", w))
			} else {
				out = append(out, fmt.Sprintf("%q is not in either overlay.", w))
			}
		}
	}
	return out
}

// statusLines renders the current spell-checker state: on/off, dictionary
// availability, and the two overlays.
func (s *SpellChecker) statusLines() []string {
	state := "on"
	if !s.Enabled() {
		state = "off"
	}
	lines := []string{"Spell checking: " + state}
	if !s.Available() {
		lines = append(lines, "Dictionary: unavailable (only the forbid list applies)")
	}

	lines = append(lines, "", "Allowed overlay:")
	lines = append(lines, indentWords(s.AllowedWords())...)
	lines = append(lines, "", "Forbidden overlay:")
	lines = append(lines, indentWords(s.ForbiddenWords())...)
	lines = append(lines, "", "Use '%help spell' for usage.")
	return lines
}

// indentWords formats a word list for display, or "(none)" when empty.
func indentWords(words []string) []string {
	if len(words) == 0 {
		return []string{"  (none)"}
	}
	return []string{"  " + strings.Join(words, ", ")}
}
