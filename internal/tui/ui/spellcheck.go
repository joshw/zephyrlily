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
	"strings"
	"unicode"

	"github.com/client9/gospell"
)

//go:embed hunspell-en_US/*
var hunspellFS embed.FS

// SpellChecker provides spell checking functionality using gospell.
type SpellChecker struct {
	enabled bool
	checker *gospell.Checker
}

// NewSpellChecker creates a new spell checker using gospell.
// It loads the embedded English Hunspell dictionary.
// If loading fails, spell checking is disabled but the checker continues to work
// (returning all words as correct).
func NewSpellChecker() *SpellChecker {
	// Load embedded Hunspell dictionary
	affData, err := hunspellFS.ReadFile("hunspell-en_US/en_US.aff")
	if err != nil {
		log.Println("Spell checking disabled (hunspell dictionary not found)")
		return &SpellChecker{enabled: false}
	}

	dicData, err := hunspellFS.ReadFile("hunspell-en_US/en_US.dic")
	if err != nil {
		log.Println("Spell checking disabled (hunspell dictionary not found)")
		return &SpellChecker{enabled: false}
	}

	// Create GoSpell from embedded dictionary data
	gs, err := gospell.NewGoSpellReader(bytes.NewReader(affData), bytes.NewReader(dicData))
	if err != nil {
		log.Println("Spell checking disabled (failed to initialize gospell):", err)
		return &SpellChecker{enabled: false}
	}

	// Create checker
	checker := gospell.NewChecker(gs)

	return &SpellChecker{
		enabled: true,
		checker: checker,
	}
}

// CheckWord checks if a single word is spelled correctly.
func (s *SpellChecker) CheckWord(word string) bool {
	if !s.enabled || word == "" {
		return true
	}
	return s.checker.Spell(word)
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
