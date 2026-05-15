package commands

import (
	"strings"

	"github.com/joshw/zephyrlily/internal/lily"
)

func init() {
	RegisterHelp(HelpTopic{
		Name: "help",
		Text: []string{
			"Show help for client commands",
			"",
			"Usage: %help [topic]",
			"",
			"With no arguments, lists all available client commands.",
			"With a topic argument, shows detailed help for that command.",
			"",
			"Example: %help version",
		},
	})
}

// handleHelp implements the %help command.
func handleHelp(state *lily.State, args []string, respond func(lines []string)) {
	// If a specific topic is requested, show detailed help
	if len(args) > 0 {
		topic := args[0]
		// Strip % prefix if present
		topic = strings.TrimPrefix(topic, "%")

		helpTopic := GetHelp(topic)
		if helpTopic != nil {
			respond(helpTopic.Text)
		} else {
			respond([]string{"No help available for: " + topic})
		}
		return
	}

	// Otherwise, list all commands with brief descriptions
	topics := ListHelp()
	lines := []string{"Client commands (handled locally):"}

	for _, topic := range topics {
		// Use the first non-empty line as description
		desc := ""
		for _, line := range topic.Text {
			if line != "" {
				desc = line
				break
			}
		}

		lines = append(lines, "  %"+topic.Name+" - "+desc)
	}

	lines = append(lines, "", "Type '%help <command>' for more details on a specific command.")

	respond(lines)
}
