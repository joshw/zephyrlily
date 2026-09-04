package ui

import (
	_ "embed"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/joshw/zephyrlily/internal/version"
)

//go:embed logo.txt
var logoArt string

// formatLogo creates the logo display with version banner as text lines.
//
// bg is the terminal's own background, or nil while it is still unknown. See
// logotransparent.go for what having it buys.
func formatLogo(bg color.Color) []string {
	// Remove ANSI cursor hide/show sequences from logo
	logo := strings.ReplaceAll(logoArt, "\x1b[?25l", "")
	logo = strings.ReplaceAll(logo, "\x1b[?25h", "")
	// Sit the art on the terminal's own background rather than its own black
	// one; see the commentary in logotransparent.go.
	logo = liftAgainst(logo, bg)
	logo = strings.TrimSpace(logo)

	logoLines := strings.Split(logo, "\n")

	// Create version banner
	bannerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("14")). // bright cyan
		Bold(true)

	banner := []string{
		"",
		bannerStyle.Render("ZephyrLily") + " " + version.String(),
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

// recolourSplash redraws the logo against the terminal's own background.
//
// The splash is built in New, before anything has asked the terminal what
// colour it is; the answer arrives later as a tea.BackgroundColorMsg. Rewriting
// the lines in place works because output is append-only, so the splash still
// occupies the first splashLines items however much has arrived since.
//
// A light background is left alone. The art is a dark picture, and lifting its
// black point to white would wash it out to nothing; dark-on-light already
// reads correctly.
func (m Model) recolourSplash(bg color.Color) Model {
	if bg == nil || m.splashLines == 0 || m.splashLines > len(m.output) {
		return m
	}
	if isLight(bg) {
		return m
	}

	lines := formatLogo(bg)
	if len(lines) != m.splashLines {
		// The art rendered to a different number of lines than it did at
		// startup, so the indices below would not line up. Leave it be.
		return m
	}
	for i, line := range lines {
		m.output[i] = OutputItem{Type: "text", Data: line}
	}

	// The cached render of every rewritten item is now stale.
	m.renderEpoch++
	return m.syncViewportContent()
}

// isLight reports whether a colour is closer to white than to black, by
// perceived luminance.
func isLight(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	// Rec. 709 luma, on 16-bit components, against the midpoint.
	return 0.2126*float64(r)+0.7152*float64(g)+0.0722*float64(b) > 0x7fff
}
