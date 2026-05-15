package commands

import (
	"fmt"

	"github.com/joshw/zephyrlily/internal/lily"
)

func init() {
	RegisterHelp(HelpTopic{
		Name: "whoami",
		Text: []string{
			"Show your user handle and name",
			"",
			"Usage: %whoami",
			"",
			"Display your current user information, including your",
			"handle, name, and blurb (if set).",
		},
	})
}

// handleWhoami implements the %whoami command.
func handleWhoami(state *lily.State, args []string, respond func(lines []string)) {
	whoami := state.Whoami
	// Look up our own entity info
	if entity := state.Get(whoami); entity != nil {
		response := fmt.Sprintf("You are: %s (%s)", whoami, entity.Name)
		if entity.Blurb != "" {
			response += fmt.Sprintf(" - [%s]", entity.Blurb)
		}
		respond([]string{response})
	} else {
		respond([]string{fmt.Sprintf("You are: %s", whoami)})
	}
}
