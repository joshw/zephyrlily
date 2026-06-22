// Package ui implements the terminal user interface for ZephyrLily.
// The contents of this file are subject to the Mozilla Public License
// Version 1.1 (the "License"); you may not use this file except in
// compliance with the License. You may obtain a copy of the License at
// http://www.mozilla.org/MPL/
//
// This file includes the Hunspell English dictionary (en_US), which is
// licensed under GPL 2.0 / LGPL 2.1 / MPL 1.1. This file is distributed
// under the terms of the Mozilla Public License, Version 1.1.
package ui

import (
	"bytes"
	"embed"
	"log"
	"sort"
	"strings"
	"unicode"

	"github.com/client9/gospell"
)

//go:embed hunspell-en_US/*
var hunspellFS embed.FS

// defaultAllowed seeds the allowed overlay with project-specific words that the
// English dictionary doesn't know about.
var defaultAllowed = []string{"zlily", "zephyrlily"}

// SpellChecker provides spell checking functionality using gospell.
//
// On top of the base dictionary it maintains two user-managed overlays of
// lower-cased words: an allow list (accepted even if the dictionary rejects
// them) and a forbid list (rejected even if the dictionary accepts them). The
// forbid list wins over the allow list. Overlay lookups are case-insensitive so
// that, e.g., "Zlily" at the start of a sentence is also accepted.
type SpellChecker struct {
	dictLoaded bool             // base dictionary initialized successfully
	off        bool             // user disabled spell checking via %spell off
	checker    *gospell.Checker // nil when dictLoaded is false
	allowed    map[string]struct{}
	forbidden  map[string]struct{}
}

// NewSpellChecker creates a new spell checker using gospell.
// It loads the embedded English Hunspell dictionary.
// If loading fails, the base dictionary is disabled but the checker continues to
// work: every word is accepted unless it appears in the forbid overlay.
func NewSpellChecker() *SpellChecker {
	s := &SpellChecker{
		allowed:   make(map[string]struct{}),
		forbidden: make(map[string]struct{}),
	}
	for _, w := range defaultAllowed {
		s.allowed[w] = struct{}{}
	}

	// Load embedded Hunspell dictionary
	affData, err := hunspellFS.ReadFile("hunspell-en_US/en_US.aff")
	if err != nil {
		log.Println("Spell checking disabled (hunspell dictionary not found)")
		return s
	}

	dicData, err := hunspellFS.ReadFile("hunspell-en_US/en_US.dic")
	if err != nil {
		log.Println("Spell checking disabled (hunspell dictionary not found)")
		return s
	}

	// Create GoSpell from embedded dictionary data
	gs, err := gospell.NewGoSpellReader(bytes.NewReader(affData), bytes.NewReader(dicData))
	if err != nil {
		log.Println("Spell checking disabled (failed to initialize gospell):", err)
		return s
	}

	s.checker = gospell.NewChecker(gs)
	s.dictLoaded = true
	return s
}

// CheckWord checks if a single word is spelled correctly. The forbid overlay
// takes precedence over the allow overlay, which takes precedence over the base
// dictionary. Overlay matching is case-insensitive.
func (s *SpellChecker) CheckWord(word string) bool {
	if s.off || word == "" {
		return true
	}
	lw := strings.ToLower(word)
	if _, ok := s.forbidden[lw]; ok {
		return false
	}
	if _, ok := s.allowed[lw]; ok {
		return true
	}
	if !s.dictLoaded {
		return true
	}
	return s.checker.Spell(word)
}

// Enabled reports whether spell checking is currently active (the user has not
// turned it off). The base dictionary may still be unavailable; see Available.
func (s *SpellChecker) Enabled() bool { return !s.off }

// Available reports whether the base English dictionary loaded successfully.
// When false, only the forbid overlay affects results.
func (s *SpellChecker) Available() bool { return s.dictLoaded }

// SetEnabled turns spell checking on or off for the session.
func (s *SpellChecker) SetEnabled(on bool) { s.off = !on }

// Allow adds word to the allow overlay (and removes it from the forbid overlay).
func (s *SpellChecker) Allow(word string) {
	lw := strings.ToLower(word)
	delete(s.forbidden, lw)
	s.allowed[lw] = struct{}{}
}

// Forbid adds word to the forbid overlay (and removes it from the allow overlay).
func (s *SpellChecker) Forbid(word string) {
	lw := strings.ToLower(word)
	delete(s.allowed, lw)
	s.forbidden[lw] = struct{}{}
}

// Remove drops word from both overlays, reverting it to the dictionary default.
// It reports whether the word was present in either overlay.
func (s *SpellChecker) Remove(word string) bool {
	lw := strings.ToLower(word)
	_, inAllow := s.allowed[lw]
	_, inForbid := s.forbidden[lw]
	delete(s.allowed, lw)
	delete(s.forbidden, lw)
	return inAllow || inForbid
}

// ResetOverlays clears both overlays back to their defaults.
func (s *SpellChecker) ResetOverlays() {
	s.allowed = make(map[string]struct{})
	s.forbidden = make(map[string]struct{})
	for _, w := range defaultAllowed {
		s.allowed[w] = struct{}{}
	}
}

// AllowedWords returns the allow overlay, sorted.
func (s *SpellChecker) AllowedWords() []string { return sortedKeys(s.allowed) }

// ForbiddenWords returns the forbid overlay, sorted.
func (s *SpellChecker) ForbiddenWords() []string { return sortedKeys(s.forbidden) }

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for w := range m {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// Word represents a word in the input with its position and spelling status.
type Word struct {
	Text       string
	Start      int
	End        int
	Misspelled bool
}

// ParseWords splits input into words and checks their spelling.
// Words inside skip regions (message targets and URL tokens) are never
// marked as misspelled.
func (s *SpellChecker) ParseWords(input string) []Word {
	skip := buildSkipRegions(input)

	var words []Word
	var currentWord strings.Builder
	wordStart := -1

	for i, r := range input {
		if unicode.IsLetter(r) || r == '\'' || r == '-' {
			if wordStart == -1 {
				wordStart = i
			}
			currentWord.WriteRune(r)
		} else {
			if wordStart != -1 {
				word := currentWord.String()
				words = append(words, Word{
					Text:       word,
					Start:      wordStart,
					End:        i,
					Misspelled: !skip[wordStart] && !s.CheckWord(word),
				})
				currentWord.Reset()
				wordStart = -1
			}
		}
	}

	// The final word has no terminating non-word character, so the user is
	// still typing it — don't check spelling yet.
	if wordStart != -1 {
		words = append(words, Word{
			Text:       currentWord.String(),
			Start:      wordStart,
			End:        len(input),
			Misspelled: false,
		})
	}

	return words
}

// buildSkipRegions returns a per-byte boolean slice; true means the byte falls
// inside a region that should never be spell-checked:
//
//   - The message target: everything before the first ';' or ':' when the input
//     is not a command (doesn't start with '/' or '%').
//   - URL tokens: runs of URL-safe characters that contain "://" or start with
//     "www.".
func buildSkipRegions(input string) []bool {
	skip := make([]bool, len(input)+1)

	// Message target: skip everything before the first ; or : separator.
	if len(input) > 0 && input[0] != '/' && input[0] != '%' {
		for i, r := range input {
			if r == ';' || r == ':' {
				for j := 0; j < i; j++ {
					skip[j] = true
				}
				break
			}
		}
	}

	// URL tokens: find "://" and mark the surrounding run of URL characters.
	for i := 0; i+2 < len(input); i++ {
		if input[i] == ':' && input[i+1] == '/' && input[i+2] == '/' {
			start := i
			for start > 0 && isURLChar(rune(input[start-1])) {
				start--
			}
			end := i + 3
			for end < len(input) && isURLChar(rune(input[end])) {
				end++
			}
			for j := start; j < end; j++ {
				skip[j] = true
			}
		}
	}

	// www. tokens: mark the whole hostname that follows.
	for i := 0; i+4 <= len(input); i++ {
		if input[i:i+4] == "www." {
			end := i + 4
			for end < len(input) && isURLChar(rune(input[end])) {
				end++
			}
			for j := i; j < end; j++ {
				skip[j] = true
			}
		}
	}

	return skip
}

// isURLChar reports whether r is a character that can appear inside a URL token.
func isURLChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) ||
		r == '.' || r == '/' || r == ':' || r == '-' || r == '_' ||
		r == '~' || r == '?' || r == '#' || r == '&' || r == '=' || r == '+'
}
