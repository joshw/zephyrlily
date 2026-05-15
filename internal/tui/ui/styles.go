package ui

import "github.com/charmbracelet/lipgloss"

// Semantic styles for different message components.
// Based on tigerlily's default color scheme.
// Using ANSI color codes 0-15 for maximum compatibility.
//
// ANSI colors: 0=black, 1=red, 2=green, 3=yellow, 4=blue, 5=magenta, 6=cyan, 7=white
//              8-15 are bright versions
var (
	// 	  'status_window'   => [qw(yellow  blue    bold  )],
	//    'input_window'    => [qw(white   black   normal)],
	//    'input_error'     => [qw(red     black   normal)],
	//    'text_window'     => [qw(white   black   normal)],
	//    'public_header'   => [qw(cyan    black   normal)],
	//    'public_sender'   => [qw(cyan    black   bold  )],
	//    'public_dest'     => [qw(cyan    black   bold  )],
	//    'public_body'     => [qw(white   black   normal)],
	//    'public_server'   => [qw(cyan    black   normal)],
	//    'private_header'  => [qw(green   black   normal)],
	//    'private_sender'  => [qw(green   black   bold  )],
	//    'private_dest'    => [qw(green   black   bold  )],
	//    'private_body'    => [qw(white   black   normal)],
	//    'private_server'  => [qw(green   black   normal)],
	//    'emote_body'      => [qw(cyan    black   normal)],
	//    'emote_dest'      => [qw(cyan    black   normal)],
	//    'emote_sender'    => [qw(cyan    black   bold  )],
	//    'emote_server'    => [qw(cyan    black   normal)],
	//    'review'          => [qw(magenta black   normal)],
	//    'user_input'      => [qw(white   black   normal)],

	// Timestamp styles
	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // bright black (gray)

	// Public message styles
	publicSenderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")). // cyan
				Bold(true)

	publicHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")) // cyan, normal

	publicBodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")) // white, normal

	// Private message styles
	privateSenderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")). // green
				Bold(true)

	privateHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")) // green, normal

	privateBodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")) // white, normal

	// Emote styles
	emoteSenderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("6")). // cyan
				Bold(true)

	emoteBodyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")) // cyan, normal

	// Blurb style
	blurbStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")) // bright black (gray)

	// Command and system messages
	commandResultStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("12")) // bright blue

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")) // red, normal (not bold per tigerlily)

	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")). // bright yellow
			Bold(true)

	// Input styling
	inputPrefixStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("2")). // green
				Bold(true)

	// Status bar styling (tigerlily: yellow on blue, bold)
	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("4")). // blue background
			Foreground(lipgloss.Color("11")). // bright yellow text
			Bold(true)
)
