package ui

import (
	"bytes"
	"container/list"
	"log"
	"os/exec"
	"strings"
	"sync"
	"unicode"
)

// SpellChecker provides spell checking functionality.
type SpellChecker struct {
	enabled bool
	command string // "aspell" or "ispell"

	// LRU cache for spell check results
	cacheMu    sync.RWMutex
	cache      map[string]bool // word -> isCorrect
	cacheOrder *list.List      // LRU order
	cacheSize  int
	maxCache   int
}

type cacheEntry struct {
	word string
}

// NewSpellChecker creates a new spell checker.
// It checks if aspell or ispell is available and disables itself if neither is found.
// Logs status messages using the standard logger.
func NewSpellChecker() *SpellChecker {
	// Try aspell first
	if cmd := exec.Command("aspell", "--version"); cmd.Run() == nil {
		log.Println("Spell checking enabled (using aspell)")
		return &SpellChecker{
			enabled:    true,
			command:    "aspell",
			cache:      make(map[string]bool),
			cacheOrder: list.New(),
			maxCache:   1000, // Cache up to 1000 words
		}
	}

	// Fall back to ispell
	if cmd := exec.Command("ispell", "-v"); cmd.Run() == nil {
		log.Println("Spell checking enabled (using ispell)")
		return &SpellChecker{
			enabled:    true,
			command:    "ispell",
			cache:      make(map[string]bool),
			cacheOrder: list.New(),
			maxCache:   1000,
		}
	}

	// Neither available, disable
	log.Println("Spell checking disabled (aspell or ispell not found)")
	log.Println("Install aspell or ispell for spell checking support")
	return &SpellChecker{enabled: false}
}

// CheckWord checks if a single word is spelled correctly.
func (s *SpellChecker) CheckWord(word string) bool {
	if !s.enabled || word == "" {
		return true
	}

	// Skip words that are all numbers or contain special characters
	if !isAlphabetic(word) {
		return true
	}

	// Check cache first
	s.cacheMu.RLock()
	if result, ok := s.cache[word]; ok {
		s.cacheMu.RUnlock()
		return result
	}
	s.cacheMu.RUnlock()

	// Not in cache, check with spell checker
	result := s.checkWordExternal(word)

	// Store in cache
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	// Evict oldest entry if cache is full
	if s.cacheSize >= s.maxCache {
		oldest := s.cacheOrder.Back()
		if oldest != nil {
			entry := oldest.Value.(*cacheEntry)
			delete(s.cache, entry.word)
			s.cacheOrder.Remove(oldest)
			s.cacheSize--
		}
	}

	// Add to cache
	s.cache[word] = result
	s.cacheOrder.PushFront(&cacheEntry{word: word})
	s.cacheSize++

	return result
}

// checkWordExternal performs the actual spell check using the external command.
func (s *SpellChecker) checkWordExternal(word string) bool {
	// Use spell checker in pipe mode for a single word check
	cmd := exec.Command(s.command, "-a") // -a for pipe mode (works for both aspell and ispell)
	cmd.Stdin = strings.NewReader(word)

	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		// If spell checker fails, assume word is correct
		return true
	}

	// Both aspell and ispell pipe output format:
	// First line is version info starting with @
	// Second line: "*" for correct, "&" or "#" for incorrect
	lines := strings.Split(out.String(), "\n")
	if len(lines) < 2 {
		return true
	}

	// Check the result line (skip version line)
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		// "*" means correctly spelled
		if strings.HasPrefix(line, "*") {
			return true
		}
		// "&" or "#" means misspelled
		if strings.HasPrefix(line, "&") || strings.HasPrefix(line, "#") {
			return false
		}
	}

	return true
}

// isAlphabetic checks if a word contains only letters (and possibly apostrophes/hyphens)
func isAlphabetic(word string) bool {
	hasLetter := false
	for _, r := range word {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else if r != '\'' && r != '-' {
			return false
		}
	}
	return hasLetter
}

// Word represents a word in the input with its position and spelling status.
type Word struct {
	Text       string
	Start      int
	End        int
	Misspelled bool
}

// ParseWords splits input into words and checks their spelling.
func (s *SpellChecker) ParseWords(input string) []Word {
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
					Misspelled: !s.CheckWord(word),
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
