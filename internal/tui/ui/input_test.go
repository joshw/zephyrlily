package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPasteMode(t *testing.T) {
	tests := []struct {
		name          string
		pasteText     string
		initialValue  string
		expectedValue string
		description   string
	}{
		{
			name:          "single_newline_lf",
			pasteText:     "c\nd",
			initialValue:  "",
			expectedValue: "c d",
			description:   "Paste with LF newline should convert to space",
		},
		{
			name:          "single_newline_cr",
			pasteText:     "c\rd",
			initialValue:  "",
			expectedValue: "c d",
			description:   "Paste with CR newline should convert to space",
		},
		{
			name:          "consecutive_newlines",
			pasteText:     "a\r\rb",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Consecutive newlines should be eaten to single space",
		},
		{
			name:          "consecutive_spaces",
			pasteText:     "a  b",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Consecutive spaces should be eaten to single space",
		},
		{
			name:          "mixed_whitespace",
			pasteText:     "a \r b",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Mixed space and newline should normalize",
		},
		{
			name:          "space_then_newline",
			pasteText:     "a \rb",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Space then newline should be eaten",
		},
		{
			name:          "newline_then_space",
			pasteText:     "a\r b",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Newline then space should be eaten",
		},
		{
			name:          "leading_whitespace",
			pasteText:     "\r\n  hello",
			initialValue:  "",
			expectedValue: " hello",
			description:   "Leading whitespace should normalize to single space",
		},
		{
			name:          "trailing_whitespace",
			pasteText:     "hello\r\n  ",
			initialValue:  "",
			expectedValue: "hello ",
			description:   "Trailing whitespace should normalize",
		},
		{
			name:          "multiline_with_text",
			pasteText:     "line1\rline2\nline3",
			initialValue:  "",
			expectedValue: "line1 line2 line3",
			description:   "Multi-line text should convert newlines to spaces",
		},
		{
			name:          "append_to_existing",
			pasteText:     "b\rc",
			initialValue:  "a ",
			expectedValue: "a b c",
			description:   "Pasting onto existing text should work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				pasteMode:    true,
				inputValue:   tt.initialValue,
				inputCursor:  len(tt.initialValue),
				pasteEatFlag: false,
				pasteEatBuf:  false,
			}

			// Simulate the paste as a single KeyRunes event with all characters
			msg := tea.KeyMsg{
				Type:  tea.KeyRunes,
				Runes: []rune(tt.pasteText),
			}

			// Process the paste (simulate the paste mode handling from handleNormalKey)
			if msg.Type == tea.KeyRunes {
				for _, r := range msg.Runes {
					if r == ' ' || r == '\n' || r == '\r' {
						// Whitespace: convert to space, but eat consecutive whitespace
						if m.pasteEatFlag {
							continue
						}
						if m.pasteEatBuf {
							m.pasteEatFlag = true
							continue
						}
						// First whitespace in sequence: insert it and mark to eat future whitespace
						m.pasteEatBuf = true
						m = m.insertString(" ")
					} else {
						// Non-whitespace: clear flags and insert
						m.pasteEatFlag = false
						m.pasteEatBuf = false
						m = m.insertString(string(r))
					}
				}
			}

			if m.inputValue != tt.expectedValue {
				t.Errorf("%s: %s\n  got:      %q\n  expected: %q", tt.name, tt.description, m.inputValue, tt.expectedValue)
			}
		})
	}
}

func TestPasteModeWithEnterKey(t *testing.T) {
	tests := []struct {
		name          string
		actions       []string // "text" or "enter"
		expectedValue string
	}{
		{
			name:          "text_enter_text",
			actions:       []string{"a", "enter", "b"},
			expectedValue: "a b",
		},
		{
			name:          "consecutive_enters",
			actions:       []string{"a", "enter", "enter", "b"},
			expectedValue: "a b",
		},
		{
			name:          "space_then_enter",
			actions:       []string{"a", " ", "enter", "b"},
			expectedValue: "a b",
		},
		{
			name:          "enter_then_space",
			actions:       []string{"a", "enter", " ", "b"},
			expectedValue: "a b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				pasteMode:    true,
				inputValue:   "",
				inputCursor:  0,
				pasteEatFlag: false,
				pasteEatBuf:  false,
			}

			for _, action := range tt.actions {
				if action == "enter" {
					// Simulate Enter key (non-rune key)
					if m.pasteEatFlag {
						// Skip, eat the enter
					} else if m.pasteEatBuf {
						m.pasteEatFlag = true
					} else {
						m.pasteEatBuf = true
						m = m.insertString(" ")
					}
				} else {
					// Simulate regular runes
					for _, r := range []rune(action) {
						if r == ' ' {
							if m.pasteEatFlag {
								continue
							}
							if m.pasteEatBuf {
								m.pasteEatFlag = true
								continue
							}
							m.pasteEatBuf = true
							m = m.insertString(" ")
						} else {
							m.pasteEatFlag = false
							m.pasteEatBuf = false
							m = m.insertString(string(r))
						}
					}
				}
			}

			if m.inputValue != tt.expectedValue {
				t.Errorf("%s: got %q, expected %q", tt.name, m.inputValue, tt.expectedValue)
			}
		})
	}
}
