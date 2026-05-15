package ui

import "github.com/charmbracelet/lipgloss"

// misspelledStyle is the style for misspelled words
var misspelledStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("1")). // red
	Underline(true)

// renderInputLine renders the input line with spell checking highlights.
func (m Model) renderInputLine() string {
	if m.input == "" {
		return promptStyle.Render(m.prompt) + " _"
	}

	// Parse words and check spelling
	words := m.spellChecker.ParseWords(m.input)

	// Build the input string with highlighting
	var result string
	lastEnd := 0

	for _, word := range words {
		// Add text before this word (spaces, punctuation)
		if word.Start > lastEnd {
			result += m.input[lastEnd:word.Start]
		}

		// Add the word with appropriate styling
		if word.Misspelled {
			result += misspelledStyle.Render(word.Text)
		} else {
			result += word.Text
		}

		lastEnd = word.End
	}

	// Add any remaining text after the last word
	if lastEnd < len(m.input) {
		result += m.input[lastEnd:]
	}

	return promptStyle.Render(m.prompt) + " " + result + "_"
}
