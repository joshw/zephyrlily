package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// tuiHelp holds help topics that are specific to the TUI client.
var tuiHelp = map[string][]string{
	"debugkeys": {
		"Toggle keypress debugging",
		"",
		"Usage: %set debug keys",
		"",
		"When enabled, every key event is logged to the debug window.",
		"Run the command again to turn it off.",
		"Tip: open the debug view with ESC G to see the key log.",
	},
	"info": {
		"Edit your info (or a discussion's info) in the TUI",
		"",
		"Usage: %info edit [target]",
		"",
		"Opens an in-TUI editor pre-populated with the current info text.",
		"If no target is given, edits your own info.",
		"Ctrl+S saves; Esc cancels without saving.",
	},
	"memo": {
		"Edit a memo in the TUI",
		"",
		"Usage: %memo edit [target] <name>",
		"",
		"Opens an in-TUI editor pre-populated with the named memo.",
		"If no target is given, edits a memo on your own memo pad.",
		"Ctrl+S saves; Esc cancels without saving.",
	},
	"page": {
		"Toggle the viewport pager",
		"",
		"Usage: %page on|off",
		"",
		"When on (the default), output longer than one screen pauses after",
		"each page so you can read it; press Enter to advance.",
		"When off, output scrolls straight to the bottom without pausing.",
		"Run '%page' with no argument to show the current setting.",
	},
	"style": {
		"Show and change color/style configuration",
		"",
		"Usage: %style [list]                table of all styles + whether each is default",
		"       %style <name>                show one style",
		"       %style <name> fg <color>     set foreground",
		"       %style <name> bg <color>     set background",
		"       %style <name> bold on|off",
		"       %style <name> underline on|off",
		"       %style <name> default        restore one style to its default",
		"       %style <name> none           make one style unstyled but visible",
		"       %style all default           restore every style to its default",
		"",
		"Colors: 0-255, names (red, cyan, brightyellow, ...), or #rrggbb.",
		"\"default\" restores the built-in value; \"none\" clears styling.",
		"Changes apply immediately and last for this session only.",
		"",
		"Tip: use zlilyStartup memo to make style changes permanent (see %help startup).",
	},
	"spell": {
		"Manage the spell checker and its word overlays",
		"",
		"Usage: %spell [list]                 show state + allowed/forbidden words",
		"       %spell on|off                 enable/disable spell checking",
		"       %spell allow <word>...        accept words the dictionary rejects",
		"       %spell forbid <word>...       reject words the dictionary accepts",
		"       %spell remove <word>...       drop words from both overlays",
		"       %spell reset                  clear overlays back to defaults",
		"",
		"The forbid list wins over the allow list, which wins over the",
		"dictionary. Matching is case-insensitive. \"zlily\" and \"zephyrlily\"",
		"are allowed by default.",
		"Changes apply immediately and last for this session only.",
		"",
		"Tip: use zlilyStartup memo to make overlays permanent (see %help startup).",
	},
	"startup": {
		"Run commands automatically on connect",
		"",
		"After you connect and log in, zlily fetches the memo named",
		"\"zlilyStartup\" from your own memo pad and runs each line as if",
		"you had typed it and pressed Enter.",
		"",
		"This runs every time you connect, including after a reconnect,",
		"since a reconnect re-establishes a fresh Lily session.",
		"Lily commands, sends, and TUI %commands all work.",
		"Lines starting with '#' are treated as comments and skipped;",
		"blank lines are ignored too.",
		"",
		"To edit it:  %memo edit zlilyStartup",
		"If the memo doesn't exist, nothing happens.",
	},
	"debug": {
		"Toggle the split-screen debug view",
		"",
		"Key binding: ESC G  (or Alt+G if your terminal supports it)",
		"",
		"The debug view splits the screen in half:",
		"  Left  - the normal output window",
		"  Right - a live message log",
		"",
		"Log entries:",
		"  SEND: - commands forwarded to the Lily server (JSON)",
		"  RECV: - messages received from the proxy (JSON)",
		"  expand query / expand: - name expansion activity",
		"",
		"While in debug mode, PgUp / PgDn scroll the right panel independently.",
		"Press ESC G again to return to the normal view.",
	},
}

// handleLocalCommand inspects line and returns local output if applicable.
func (m Model) handleLocalCommand(line string) (localOutput []string, handled bool, cmd tea.Cmd) {
	if len(line) == 0 {
		return nil, false, nil
	}

	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil, false, nil
	}
	command := parts[0]
	args := parts[1:]

	// Intercept Lily /info set and /memo set
	if command == "/info" && len(args) > 0 && args[0] == "set" {
		return []string{"Use %info edit [target] to edit your info."}, true, nil
	}
	if command == "/memo" && len(args) > 0 && args[0] == "set" {
		return []string{"Use %memo edit [target] <name> to edit a memo."}, true, nil
	}

	if line[0] != '%' {
		return nil, false, nil
	}

	switch command {
	case "%help":
		if len(args) == 0 {
			return tuiHelpSummary(), false, nil
		}
		topic := args[0]
		if topic == "keys" {
			return m.keys.KeyBindingHelp(), true, nil
		}
		if lines, ok := tuiHelp[topic]; ok {
			return lines, true, nil
		}
		return nil, false, nil

	case "%info":
		if len(args) == 0 || args[0] != "edit" {
			return nil, false, nil
		}
		target := "me"
		if len(args) >= 2 {
			target = args[1]
		}
		meta := editMeta{contentType: "info", target: target}
		return nil, true, m.fetchContentCmd(meta)

	case "%memo":
		if len(args) == 0 || args[0] != "edit" {
			return nil, false, nil
		}
		editArgs := args[1:]
		var target, name string
		switch len(editArgs) {
		case 0:
			return []string{"Usage: %memo edit [target] <name>"}, true, nil
		case 1:
			target, name = "me", editArgs[0]
		default:
			target, name = editArgs[0], editArgs[1]
		}
		meta := editMeta{contentType: "memo", target: target, name: name}
		return nil, true, m.fetchContentCmd(meta)

	case "%style":
		return handleStyleCommand(args), true, nil

	case "%spell":
		return m.spellChecker.HandleCommand(args), true, nil
	}

	return nil, false, nil
}

// tuiHelpSummary builds the short listing injected above the proxy's %help output.
func tuiHelpSummary() []string {
	lines := []string{
		"TUI2-specific commands (use '%help <topic>' for details):",
		"  keys - Key binding reference",
	}
	for topic, text := range tuiHelp {
		desc := ""
		for _, l := range text {
			if l != "" {
				desc = l
				break
			}
		}
		lines = append(lines, "  "+topic+" - "+desc)
	}
	lines = append(lines, "")
	return lines
}
