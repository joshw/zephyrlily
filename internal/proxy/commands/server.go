package commands

import (
	"fmt"

	"github.com/joshw/zephyrlily/internal/lily"
)

func init() {
	RegisterHelp(HelpTopic{
		Name: "server",
		Text: []string{
			"Show connected server information",
			"",
			"Usage: %server",
			"",
			"Display information about the connected Lily server,",
			"including the server name and protocol version.",
		},
	})
}

// handleServer implements the %server command.
func handleServer(state *lily.State, args []string, respond func(lines []string)) {
	response := fmt.Sprintf("Connected to: %s (version %s)", state.Name, state.Version)
	respond([]string{response})
}
