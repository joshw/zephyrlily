package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// tuiHelp holds help topics that are specific to the TUI client.
// Entries here are served by the TUI itself and never forwarded to the proxy.
// Add new topics by inserting into this map.
var tuiHelp = map[string][]string{
	"keys": {
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

// handleLocalCommand inspects line and returns:
//
//   - localOutput: lines to inject into the output window immediately (nil = nothing)
//   - handled: if true the command was fully handled locally and must NOT be
//     forwarded to the proxy; if false the proxy should still receive the command.
//   - cmd: an optional async Bubble Tea command (e.g. fetch content) to run.
//
// This lets bare "%help" both inject TUI topics and forward to the proxy, while
// "%help debug", "%info edit", and "%memo edit" are handled entirely by the TUI.
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

	// Intercept Lily /info set and /memo set before they reach the server.
	// These must be checked before the '%'-only guard below.
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
			// Bare "%help" — inject TUI section and forward to proxy.
			return tuiHelpSummary(), false, nil
		}
		topic := args[0]
		if lines, ok := tuiHelp[topic]; ok {
			return lines, true, nil
		}
		return nil, false, nil // proxy handles unknown topics

	case "%info":
		if len(args) == 0 || args[0] != "edit" {
			return nil, false, nil // forward unknown %info subcommands to proxy
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
	}

	return nil, false, nil
}

// tuiHelpSummary builds the short listing injected above the proxy's %help output.
func tuiHelpSummary() []string {
	lines := []string{"TUI-specific commands (use '%help <topic>' for details):"}
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

// escapeKeyString replaces control characters in a key string with printable
// representations so that debug log lines containing newlines or other
// non-printable bytes don't break the single-line debug window rendering.
func escapeKeyString(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteString(fmt.Sprintf(`\x%02x`, r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

