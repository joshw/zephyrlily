package commands

import (
	"fmt"

	"github.com/joshw/zephyrlily/internal/lily"
	"github.com/joshw/zephyrlily/internal/version"
)

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
func handleVersion(_ *lily.State, _ []string, respond func(lines []string)) {
	respond([]string{fmt.Sprintf("zlily version %s", version.String())})
}
