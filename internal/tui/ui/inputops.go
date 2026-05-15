package ui

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// isWordChar returns true for alphanumeric and underscore characters.
func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// wordStartBefore returns the byte offset of the start of the word ending at or before pos.
func wordStartBefore(s string, pos int) int {
	for pos > 0 && !isWordChar(s[pos-1]) {
		pos--
	}
	for pos > 0 && isWordChar(s[pos-1]) {
		pos--
	}
	return pos
}

// wordEndAfter returns the byte offset just past the word starting at or after pos.
func wordEndAfter(s string, pos int) int {
	for pos < len(s) && !isWordChar(s[pos]) {
		pos++
	}
	for pos < len(s) && isWordChar(s[pos]) {
		pos++
	}
	return pos
}

// insertString inserts s at the cursor position and advances the cursor.
func (m Model) insertString(s string) Model {
	m.input = m.input[:m.cursor] + s + m.input[m.cursor:]
	m.cursor += len(s)
	return m
}

// moveCursorBack moves the cursor one rune to the left.
func (m Model) moveCursorBack() Model {
	if m.cursor > 0 {
		_, size := utf8.DecodeLastRuneInString(m.input[:m.cursor])
		m.cursor -= size
	}
	return m
}

// moveCursorForward moves the cursor one rune to the right.
func (m Model) moveCursorForward() Model {
	if m.cursor < len(m.input) {
		_, size := utf8.DecodeRuneInString(m.input[m.cursor:])
		m.cursor += size
	}
	return m
}

// deleteBack deletes the rune before the cursor.
func (m Model) deleteBack() Model {
	if m.cursor > 0 {
		_, size := utf8.DecodeLastRuneInString(m.input[:m.cursor])
		m.input = m.input[:m.cursor-size] + m.input[m.cursor:]
		m.cursor -= size
	}
	return m
}

// deleteForward deletes the rune at the cursor.
func (m Model) deleteForward() Model {
	if m.cursor < len(m.input) {
		_, size := utf8.DecodeRuneInString(m.input[m.cursor:])
		m.input = m.input[:m.cursor] + m.input[m.cursor+size:]
	}
	return m
}

// transposeChars swaps the char before the cursor with the char at the cursor,
// then advances the cursor. If at the end, swaps the last two chars.
func (m Model) transposeChars() Model {
	if len(m.input) < 2 {
		return m
	}

	pos := m.cursor
	// If at end, move back one so we can swap the last two chars
	if pos >= len(m.input) {
		_, size := utf8.DecodeLastRuneInString(m.input[:pos])
		pos -= size
	}
	if pos == 0 {
		return m
	}

	_, prevSize := utf8.DecodeLastRuneInString(m.input[:pos])
	_, nextSize := utf8.DecodeRuneInString(m.input[pos:])

	prev := m.input[pos-prevSize : pos]
	next := m.input[pos : pos+nextSize]

	m.input = m.input[:pos-prevSize] + next + prev + m.input[pos+nextSize:]
	m.cursor = pos - prevSize + nextSize + prevSize
	return m
}

// transposeWords swaps the word before the cursor with the word after,
// moving the cursor past the second word.
func (m Model) transposeWords() Model {
	// Find the word before cursor
	end1 := m.cursor
	for end1 > 0 && !isWordChar(m.input[end1-1]) {
		end1--
	}
	if end1 == 0 {
		return m
	}
	start1 := end1
	for start1 > 0 && isWordChar(m.input[start1-1]) {
		start1--
	}

	// Find the word after cursor
	start2 := m.cursor
	for start2 < len(m.input) && !isWordChar(m.input[start2]) {
		start2++
	}
	if start2 >= len(m.input) {
		return m
	}
	end2 := start2
	for end2 < len(m.input) && isWordChar(m.input[end2]) {
		end2++
	}

	word1 := m.input[start1:end1]
	word2 := m.input[start2:end2]
	between := m.input[end1:start2]

	m.input = m.input[:start1] + word2 + between + word1 + m.input[end2:]
	m.cursor = start1 + len(word2) + len(between) + len(word1)
	return m
}

// capitalizeWord skips non-word chars, then capitalizes the next word
// (uppercases first char, lowercases rest), moving cursor to end of word.
func (m Model) capitalizeWord() Model {
	pos := m.cursor
	for pos < len(m.input) && !isWordChar(m.input[pos]) {
		pos++
	}
	if pos >= len(m.input) {
		return m
	}
	start := pos
	for pos < len(m.input) && isWordChar(m.input[pos]) {
		pos++
	}
	word := m.input[start:pos]
	word = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	m.input = m.input[:start] + word + m.input[pos:]
	m.cursor = pos
	return m
}

// upcaseWord skips non-word chars, then uppercases the next word,
// moving cursor to end of word.
func (m Model) upcaseWord() Model {
	pos := m.cursor
	for pos < len(m.input) && !isWordChar(m.input[pos]) {
		pos++
	}
	if pos >= len(m.input) {
		return m
	}
	start := pos
	for pos < len(m.input) && isWordChar(m.input[pos]) {
		pos++
	}
	word := strings.ToUpper(m.input[start:pos])
	m.input = m.input[:start] + word + m.input[pos:]
	m.cursor = pos
	return m
}

// downcaseWord skips non-word chars, then lowercases the next word,
// moving cursor to end of word.
func (m Model) downcaseWord() Model {
	pos := m.cursor
	for pos < len(m.input) && !isWordChar(m.input[pos]) {
		pos++
	}
	if pos >= len(m.input) {
		return m
	}
	start := pos
	for pos < len(m.input) && isWordChar(m.input[pos]) {
		pos++
	}
	word := strings.ToLower(m.input[start:pos])
	m.input = m.input[:start] + word + m.input[pos:]
	m.cursor = pos
	return m
}

// historyPrev moves to the previous (older) history entry.
func (m Model) historyPrev() Model {
	if len(m.history) == 0 {
		return m
	}
	if m.historyPos == -1 {
		m.historySave = m.input
		m.historyPos = len(m.history) - 1
	} else if m.historyPos > 0 {
		m.historyPos--
	}
	m.input = m.history[m.historyPos]
	m.cursor = len(m.input)
	return m
}

// historyNext moves to the next (newer) history entry, restoring live input when past the end.
func (m Model) historyNext() Model {
	if m.historyPos == -1 {
		return m
	}
	m.historyPos++
	if m.historyPos >= len(m.history) {
		m.historyPos = -1
		m.input = m.historySave
		m.historySave = ""
	} else {
		m.input = m.history[m.historyPos]
	}
	m.cursor = len(m.input)
	return m
}

// addHistoryEntry appends cmd to history if non-empty and not a duplicate of the last entry.
func (m Model) addHistoryEntry(cmd string) Model {
	if cmd == "" {
		return m
	}
	if len(m.history) == 0 || m.history[len(m.history)-1] != cmd {
		m.history = append(m.history, cmd)
	}
	return m
}

// enterSearch enters incremental search mode.
func (m Model) enterSearch(backward bool) Model {
	m.searchMode = true
	m.searchBack = backward
	m.searchBuf = ""
	m.searchSave = m.input
	m.searchIdx = -1
	return m
}

// updateSearchResult searches history for the current searchBuf and updates model.
func (m Model) updateSearchResult() Model {
	if m.searchBuf == "" {
		m.input = m.searchSave
		m.cursor = len(m.input)
		return m
	}

	if m.searchBack {
		// Search backward: start from searchIdx-1 (or from end if searchIdx==-1)
		start := len(m.history) - 1
		if m.searchIdx >= 0 {
			start = m.searchIdx - 1
		}
		for i := start; i >= 0; i-- {
			if strings.Contains(m.history[i], m.searchBuf) {
				m.searchIdx = i
				m.input = m.history[i]
				m.cursor = len(m.input)
				return m
			}
		}
	} else {
		// Search forward: start from searchIdx+1 (or from beginning if searchIdx==-1)
		start := 0
		if m.searchIdx >= 0 {
			start = m.searchIdx + 1
		}
		for i := start; i < len(m.history); i++ {
			if strings.Contains(m.history[i], m.searchBuf) {
				m.searchIdx = i
				m.input = m.history[i]
				m.cursor = len(m.input)
				return m
			}
		}
	}
	// No match found — keep current display but don't update searchIdx
	return m
}

// handleSearchKey processes a key event while in incremental search mode.
func (m Model) handleSearchKey(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "enter", "ctrl+m", "ctrl+j":
		// Accept current match, exit search
		m.searchMode = false
		m.cursor = len(m.input)

	case "esc", "ctrl+g":
		// Cancel search, restore saved input
		m.searchMode = false
		m.input = m.searchSave
		m.cursor = len(m.input)

	case "ctrl+r":
		// Search for older match: set searchIdx to one before current, then search backward.
		if m.searchIdx > 0 {
			m.searchIdx--
			m = m.updateSearchResult()
		}

	case "ctrl+s":
		// Search for newer match: set searchIdx to one after current, then search forward.
		if m.searchIdx >= 0 && m.searchIdx < len(m.history)-1 {
			m.searchIdx++
			m = m.updateSearchResult()
		}

	case "backspace", "ctrl+h":
		// Remove last rune from searchBuf
		if len(m.searchBuf) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.searchBuf)
			m.searchBuf = m.searchBuf[:len(m.searchBuf)-size]
		}
		m.searchIdx = -1
		m = m.updateSearchResult()

	default:
		if msg.Type == tea.KeyRunes {
			m.searchBuf += string(msg.Runes)
			m.searchIdx = -1
			m = m.updateSearchResult()
		}
	}
	return m
}
