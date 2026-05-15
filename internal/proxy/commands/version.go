package commands

import "github.com/joshw/zephyrlily/internal/lily"

func init() {
	RegisterHelp(HelpTopic{
		Name: "version",
		Text: []string{
			"Show proxy version",
			"",
			"Usage: %version",
			"",
			"Display the version of the zlily proxy.",
		},
	})
}

// handleVersion implements the %version command.
func handleVersion(state *lily.State, args []string, respond func(lines []string)) {
	respond([]string{"zlily proxy version 0.1.0"})
}
