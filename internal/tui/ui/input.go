package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

var (
	misspelledStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Underline(true)

	cursorStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("7")).
			Foreground(lipgloss.Color("0"))
)

// inputPromptText returns the text to display as the input prompt.
func (m Model) inputPromptText() string {
	if m.reconnectPrompt {
		return "Reconnect? (Y/n)"
	}
	if m.searchMode {
		dir := "i-search"
		if m.searchBack {
			dir = "reverse-i-search"
		}
		return fmt.Sprintf("(%s)`%s':", dir, m.searchBuf)
	}
	if m.pasteMode {
		return "Paste:"
	}
	return m.prompt
}

// inputPromptDisplayWidth returns the terminal columns consumed by the prompt,
// including the "▸ " separator (2 columns) embedded inside the styled render.
func (m Model) inputPromptDisplayWidth() int {
	return len(m.inputPromptText()) + 2
}

// inputFirstLineWidth returns the columns available for input text on line 0
// (after the prompt).
func (m Model) inputFirstLineWidth() int {
	w := m.width - m.inputPromptDisplayWidth()
	if w < 1 {
		w = 1
	}
	return w
}

// inputTotalLines returns the total display lines needed for m.input, including
// one reserved cell for the cursor.
func (m Model) inputTotalLines() int {
	if m.width <= 0 {
		return 1
	}
	firstWidth := m.inputFirstLineWidth()
	n := len(m.input) + 1 // +1 reserves a cell for the cursor
	if n <= firstWidth {
		return 1
	}
	rw := m.width
	return 1 + (n-firstWidth+rw-1)/rw
}

// inputCursorLine returns the 0-based index of the wrapped line that m.cursor
// falls on.
func (m Model) inputCursorLine() int {
	if m.width <= 0 {
		return 0
	}
	firstWidth := m.inputFirstLineWidth()
	c := m.cursor
	if c < firstWidth {
		return 0
	}
	rw := m.width
	return 1 + (c-firstWidth)/rw
}

// adjustInputScroll returns a copy of m with inputScroll updated so that the
// cursor line is within the visible input area.
func (m Model) adjustInputScroll() Model {
	if m.width == 0 || m.height == 0 {
		return m
	}
	cursorLine := m.inputCursorLine()
	maxVisible := m.height / 2
	if maxVisible < 1 {
		maxVisible = 1
	}
	if cursorLine < m.inputScroll {
		m.inputScroll = cursorLine
	} else if cursorLine >= m.inputScroll+maxVisible {
		m.inputScroll = cursorLine - maxVisible + 1
	}
	if m.inputScroll < 0 {
		m.inputScroll = 0
	}
	return m
}

// renderInputArea renders the input area as a slice of visibleLines display lines.
// Line 0 is prefixed with the styled prompt; continuation lines fill m.width.
// Scroll position within the input area is governed by m.inputScroll.
func (m Model) renderInputArea(visibleLines int) []string {
	if visibleLines < 1 {
		visibleLines = 1
	}

	// The trailing space is inside the Render call so it shares the same ANSI
	// reset as the prompt, acting as a separator that prevents the background
	// color of the adjacent cursor block from bleeding into the prompt glyph.
	promptRendered := promptStyle.Render(m.inputPromptText() + "▸ ")
	firstWidth := m.inputFirstLineWidth()
	rw := m.width
	if rw < 1 {
		rw = 1
	}

	// Build a per-byte misspelled lookup for O(1) access in the render loop.
	misspelledAt := make([]bool, len(m.input)+1)
	for _, w := range m.spellChecker.ParseWords(m.input) {
		if w.Misspelled {
			for i := w.Start; i < w.End; i++ {
				misspelledAt[i] = true
			}
		}
	}

	cursor := m.cursor
	cursorLine := m.inputCursorLine()

	// lineStart returns the byte offset in m.input where line k begins.
	lineStart := func(k int) int {
		if k == 0 {
			return 0
		}
		return firstWidth + (k-1)*rw
	}

	// lineEnd returns the byte offset where line k ends (exclusive, ≤ len(input)).
	lineEnd := func(k int) int {
		var end int
		if k == 0 {
			end = firstWidth
		} else {
			end = firstWidth + k*rw
		}
		if end > len(m.input) {
			end = len(m.input)
		}
		return end
	}

	out := make([]string, visibleLines)
	for i := range out {
		lineIdx := m.inputScroll + i
		var sb strings.Builder

		if lineIdx == 0 {
			sb.WriteString(promptRendered)
		}

		start := lineStart(lineIdx)
		end := lineEnd(lineIdx)

		for j := start; j < end; {
			_, size := utf8.DecodeRuneInString(m.input[j:])
			ch := m.input[j : j+size]
			switch {
			case j == cursor:
				sb.WriteString(cursorStyle.Render(ch))
			case misspelledAt[j]:
				sb.WriteString(misspelledStyle.Render(ch))
			default:
				sb.WriteString(ch)
			}
			j += size
		}

		// Cursor past end of input: solid box (background-colored space, no glyph).
		if cursor >= len(m.input) && lineIdx == cursorLine {
			sb.WriteString(cursorStyle.Render(" "))
		}

		out[i] = sb.String()
	}

	return out
}

// renderInputLine renders the input as a single display line (used by callers
// that haven't switched to the multi-line renderInputArea path yet).
func (m Model) renderInputLine() string {
	lines := m.renderInputArea(1)
	if len(lines) > 0 {
		return lines[0]
	}
	return ""
}
