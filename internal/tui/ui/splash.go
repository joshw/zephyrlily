package ui

import (
	_ "embed"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

//go:embed logo.txt
var logoArt string

const version = "0.2.0"

// formatLogo creates the logo display with version banner as text lines.
func formatLogo() []string {
	// Remove ANSI cursor hide/show sequences from logo
	logo := strings.ReplaceAll(logoArt, "\x1b[?25l", "")
	logo = strings.ReplaceAll(logo, "\x1b[?25h", "")
	logo = strings.TrimSpace(logo)

	logoLines := strings.Split(logo, "\n")

	// Create version banner
	bannerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("14")). // bright cyan
		Bold(true)

	banner := []string{
		"",
		bannerStyle.Render("ZephyrLily") + " v" + version,
		"Lily Chat Client (TUI)",
		"",
	}

	// Calculate logo width for padding
	logoWidth := 0
	for _, line := range logoLines {
		if w := lipgloss.Width(line); w > logoWidth {
			logoWidth = w
		}
	}

	// Combine logo and banner side by side
	var result []string
	maxLines := len(logoLines)
	if len(banner) > maxLines {
		maxLines = len(banner)
	}

	for i := 0; i < maxLines; i++ {
		var line strings.Builder

		// Left side: logo
		logoLine := ""
		if i < len(logoLines) {
			logoLine = logoLines[i]
		}
		line.WriteString(logoLine)

		// Padding between logo and banner
		currentWidth := lipgloss.Width(logoLine)
		padding := logoWidth - currentWidth + 4
		line.WriteString(strings.Repeat(" ", padding))

		// Right side: banner
		if i < len(banner) {
			line.WriteString(banner[i])
		}

		result = append(result, line.String())
	}

	// Add separator
	result = append(result, "")

	return result
}
