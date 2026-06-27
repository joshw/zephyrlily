package commands

// %info and %memo are client-side commands: each client implements them
// locally. The proxy deliberately does not register them in the command
// Registry, so dispatchLine forwards them to the client as a "clientcommand"
// for local execution. (Registering a stub here would intercept the command
// before the client could handle it, breaking %info/%memo lines that originate
// on the proxy — e.g. alias expansions and zlilyStartup memo replay.) Only the
// help topics are registered here.
func init() {
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
