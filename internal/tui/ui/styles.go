package ui

import "github.com/charmbracelet/lipgloss"

// Semantic styles for different message components.
// Based on tigerlily's default color scheme.
// Using ANSI color codes 0-15 for maximum compatibility.
//
// ANSI colors: 0=black, 1=red, 2=green, 3=yellow, 4=blue, 5=magenta, 6=cyan, 7=white
//
//	8-15 are bright versions
var (
	// Generic color for slcp-derived messages
	slcpBodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")) // yellow, normal

	// Public message styles
	publicSenderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")). // cyan
				Bold(true)

	publicHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")) // cyan, normal

	publicTimestampStyle = publicHeaderStyle

	publicBodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")) // white, normal

	// Private message styles
	privateSenderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")). // green
				Bold(true)

	privateHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")) // green, normal

	privateTimestampStyle = privateHeaderStyle

	privateBodyStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("7")) // white, normal

	// Emote styles
	emoteSenderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")). // cyan
				Bold(true)

	emoteBodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")) // cyan, normal

	// Blurb styles — context-matched to their message type
	blurbStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // gray (default / system events)

	publicBlurbStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")) // cyan, matching public color

	privateBlurbStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")) // green, matching private color

	// Command and system messages
	commandResultStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("12")) // bright blue

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")) // red, normal (not bold per tigerlily)

	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")). // bright yellow
			Bold(true)

	// Input styling in text window
	inputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")) // white, normal

	// Status bar styling (tigerlily: yellow on blue, bold)
	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("4")).  // blue background
			Foreground(lipgloss.Color("11")). // bright yellow text
			Bold(true)

	// Log message severity styles
	logInfoSeverityStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3")) // yellow

	logErrorSeverityStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1")) // red

	logPrefixStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // gray
)
