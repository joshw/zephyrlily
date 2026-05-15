// Package commands implements client commands handled locally by the proxy.
//
// To add a new command:
//  1. Create a new .go file in this package (e.g., mycommand.go)
//  2. Register help in an init() function using RegisterHelp()
//  3. Implement your handler function
//  4. Add your handler to the Registry map in this file
//
// Example:
//
//	func init() {
//	    RegisterHelp(HelpTopic{
//	        Name: "mycommand",
//	        Text: []string{
//	            "Brief description shown in %help list",
//	            "",
//	            "Usage: %mycommand [args]",
//	            "",
//	            "Detailed help text goes here.",
//	        },
//	    })
//	}
//
//	func handleMyCommand(state *lily.State, args []string, respond func(lines []string)) {
//	    respond([]string{"Command output goes here"})
//	}
//
// Then add to Registry: "%mycommand": handleMyCommand,
//
// Note: Help topic names should NOT include the % prefix, but command
// names in the Registry should include it.
package commands

import (
	"sort"
	"strings"

	"github.com/joshw/zephyrlily/internal/lily"
)

// Handler is a function that processes a client command.
// It receives the session state, command arguments, and a function to send the response.
type Handler func(state *lily.State, args []string, respond func(lines []string))

// HelpTopic represents a help entry for a command or topic.
type HelpTopic struct {
	Name string   // Command name WITHOUT % prefix (e.g., "version")
	Text []string // Help text lines
}

// Registry maps command names to their handlers.
var Registry = map[string]Handler{
	"%version": handleVersion,
	"%help":    handleHelp,
	"%server":  handleServer,
	"%whoami":  handleWhoami,
}

// helpRegistry stores help topics for commands.
var helpRegistry = make(map[string]HelpTopic)

// RegisterHelp registers a help topic for a command.
// Typically called from init() functions in command files.
func RegisterHelp(topic HelpTopic) {
	helpRegistry[topic.Name] = topic
}

// GetHelp returns the help topic for a given name, or nil if not found.
func GetHelp(name string) *HelpTopic {
	if topic, ok := helpRegistry[name]; ok {
		return &topic
	}
	return nil
}

// ListHelp returns a sorted list of all registered help topics.
func ListHelp() []HelpTopic {
	topics := make([]HelpTopic, 0, len(helpRegistry))
	for _, topic := range helpRegistry {
		topics = append(topics, topic)
	}
	sort.Slice(topics, func(i, j int) bool {
		return topics[i].Name < topics[j].Name
	})
	return topics
}

// Execute runs a client command and sends the response.
// The respond callback receives the lines of output from the command.
func Execute(state *lily.State, cmd string, respond func(lines []string)) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	command := parts[0]
	args := parts[1:]

	handler, ok := Registry[command]
	if !ok {
		// Unknown command
		respond([]string{"Unknown client command: " + command + " (try %help)"})
		return
	}

	// Execute the handler
	handler(state, args, respond)
}
