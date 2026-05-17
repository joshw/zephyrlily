package commands

import "github.com/joshw/zephyrlily/internal/lily"

func init() {
	Registry["%info"] = handleInfoCmd
	Registry["%memo"] = handleMemoCmd

	RegisterHelp(HelpTopic{
		Name: "info",
		Text: []string{
			"Edit an info file",
			"",
			"Usage: %info edit [target]",
			"",
			"This command must be implemented by each client.",
		},
	})
	RegisterHelp(HelpTopic{
		Name: "memo",
		Text: []string{
			"Edit a memo",
			"",
			"Usage: %memo edit [target] <name>",
			"",
			"This command must be implemented by each client.",
		},
	})
}

func handleInfoCmd(_ *lily.State, _ []string, respond func([]string)) {
	respond([]string{
		"(%info is a client-side command not yet implemented for this client.)",
		"(TUI clients: use %info edit [target] instead.)",
	})
}

func handleMemoCmd(_ *lily.State, _ []string, respond func([]string)) {
	respond([]string{
		"(%memo is a client-side command not yet implemented for this client.)",
		"(TUI clients: use %memo edit [target] <name> instead.)",
	})
}
