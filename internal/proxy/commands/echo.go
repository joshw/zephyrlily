package commands

func init() {
	RegisterHelp(HelpTopic{
		Name: "echo",
		Text: []string{
			"Echo text to the screen",
			"",
			"Usage: %echo [text]",
			"",
			"Prints text in your own scrollback, exactly as written. Nothing is",
			"sent to the server, so nobody else sees it. With no text, prints a",
			"blank line.",
			"",
			"Most useful inside aliases and the zlilyStartup memo, to label what",
			"a command does or to separate sections of output.",
			"",
			"Examples:",
			"  %echo -- reconnected --",
			"  %alias morning %echo Good morning!\\n/who here",
		},
	})
}

// Echo implements the %echo command. text is the raw remainder of the command
// line (trimmed of surrounding whitespace) and is printed verbatim: %echo is not
// a Registry handler because those receive Fields-split arguments, which would
// collapse any run of spaces the user lined their text up with.
func Echo(text string, respond func(lines []string)) {
	respond([]string{text})
}
