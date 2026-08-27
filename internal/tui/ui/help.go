package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/cmdarg"
)

// tuiHelp holds help topics that are specific to the TUI client.
var tuiHelp = map[string][]string{
	"snapshot": {
		"Write a diagnostic snapshot for bug reports",
		"",
		"Usage: %debug snapshot [path]",
		"",
		"Writes the TUI's internal state to a file (default:",
		"~/zlily-debug-<timestamp>.txt): terminal geometry, input-line state,",
		"recent input events, proxy traffic metadata, responsiveness metrics,",
		"the rendered frame, and the raw bytes recently sent to the terminal.",
		"",
		"It also measures the terminal itself, which is the half a bug report",
		"otherwise misses. Knowing only what zlily sent, a display bug always",
		"looks like 'the bytes were fine'. So the snapshot also records the",
		"size the kernel reports (flagged if it disagrees with what zlily",
		"believes), where the terminal says the cursor is, and - when running",
		"inside GNU screen - screen's own dump of what is on the display.",
		"",
		"Those three are taken BEFORE the snapshot repaints the screen, since",
		"a repaint is what clears this kind of corruption. If the display is",
		"wrong, run %debug snapshot before doing anything to fix it.",
		"Attach it to a bug report for hard-to-reproduce display or",
		"performance issues.",
		"",
		"The file contains recent typed input and screen content - review",
		"before sharing.",
	},
	"perf": {
		"Show how responsive this session has been over time",
		"",
		"Usage: %debug perf",
		"",
		"Prints per-operation latency (typing, scrolling, incoming events,",
		"repaints) for the session's lifetime, then a trend table with one row",
		"per time window: p95 latency per operation alongside heap size,",
		"goroutine count, scrollback size and bytes written to the terminal.",
		"",
		"Use it when the client feels sluggish: if the p95 columns climb from",
		"the oldest row to the newest, the slowdown is real and the gauges say",
		"what grew with it. The same table is included in %debug snapshot.",
		"",
		"Set ZLILY_PERF_WINDOW (e.g. 10s) before starting zlily to collect the",
		"trend at a finer resolution than the default one-minute windows.",
	},
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
	"linkpreview": {
		"Toggle link previews in the input line",
		"",
		"Usage: %linkpreview on|off",
		"",
		"When on (the default), typing or pasting a URL fetches what the page",
		"says about itself and shows it in gray after the URL. The gray text is",
		"not part of your message: press Tab to accept every preview on the line",
		"and turn it into real text, or just press Enter and it is dropped.",
		"With the cursor at the end of a previewed URL, Backspace removes that",
		"one preview.",
		"",
		"A URL whose page offers nothing usable shows '(no preview available)'",
		"instead. Tab steps over it — there is nothing to accept — so it can",
		"never end up in a message. Backspace clears it like any other preview.",
		"",
		"Previews come from the page's own metadata, so nothing you type is sent",
		"to a language model — but the page IS fetched, which tells its host",
		"someone is interested in that URL. Turn this off before pasting an",
		"internal address or a single-use link.",
		"Use '%linkpreview' with no argument to show the current setting.",
	},
	"shorten": {
		"Choose which URL shortener M-s uses",
		"",
		"Usage: %shorten [da.gd|tinyurl|s.u13.net]",
		"",
		"M-s replaces the first URL before the cursor with a short one, followed",
		"by the original site in brackets:",
		"",
		"  https://da.gd/wo4fk [arstechnica.com]",
		"",
		"The bracketed host is the point of it — a bare short link tells a reader",
		"nothing about where it goes. Nothing is shortened automatically, and the",
		"substitution happens in the input line, so you see exactly what will be",
		"sent and can edit it like any other text. If the shortener fails, the",
		"error is printed and your original URL is left alone.",
		"",
		"Services:",
		"  da.gd      the default; no account needed",
		"  tinyurl    no account needed; more durable, more often blocklisted",
		"  s.u13.net  tigerlily's cj.pl shortener. It refuses POSTs from outside",
		"             its own network, so it fails today no matter what; set",
		"             ZLILY_SHORTEN_API_KEY if a credential is ever issued.",
		"",
		"Link previews follow a short URL to what it points at, so the preview",
		"describes the destination rather than the shortener. That works for a",
		"short link you paste as well as one you made: da.gd is asked what the",
		"link stands for rather than being followed, because it answers anything",
		"browser-shaped with a click-through page instead of a redirect.",
		"",
		"Shortening sends the URL to that service, which keeps it for as long as",
		"the short link works. Don't shorten an internal address or a link",
		"carrying a single-use token.",
		"Use '%shorten' with no argument to show the current setting.",
	},
	"page": {
		"Toggle the viewport pager",
		"",
		"Usage: %page on|off",
		"",
		"When on (the default), output longer than one screen pauses after",
		"each page so you can read it; press Enter to advance.",
		"When off, output scrolls straight to the bottom without pausing.",
		"Use '%page' with no argument to show the current setting.",
	},
	"mouse": {
		"Toggle mouse support (wheel scrolling and click-to-position)",
		"",
		"Usage: %mouse on|off",
		"       M-m  (toggles the same setting)",
		"",
		"When on, the wheel scrolls the output and clicking in the input line",
		"moves the cursor to that spot. Off by default.",
		"Use '%mouse' with no argument to show the current setting.",
		"While it is on, the status bar shows [M].",
		"",
		"The catch: a terminal can either report the mouse to the application",
		"or use it for its own click-drag text selection, never both. So with",
		"mouse mode on, selecting text to copy needs a bypass modifier held",
		"down while you drag:",
		"  - most terminals (xterm, GNOME Terminal, Windows Terminal): Shift",
		"  - iTerm2: Option (⌥)",
		"  - macOS Terminal.app: Fn (or Shift)",
		"If yours is not listed, or you would rather not bother, press M-m to",
		"toggle mouse mode off, select normally, then M-m to turn it back on.",
		"",
		"Put '%mouse on' in your zlilyStartup memo to enable it at every login.",
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
		"To re-read and re-run it now:  %startup",
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
		"",
		"See also: %debug snapshot ('%help snapshot') to capture diagnostic",
		"state to a file for bug reports.",
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
	// Command names and their subcommands match case-insensitively; the
	// arguments after them (memo names, edit targets) keep their case.
	command := cmdarg.Fold(parts[0])
	args := parts[1:]

	// Intercept Lily /info set and /memo set
	if command == "/info" && len(args) > 0 && cmdarg.Is(args[0], "set") {
		return []string{"Use %info edit [target] to edit your info."}, true, nil
	}
	if command == "/memo" && len(args) > 0 && cmdarg.Is(args[0], "set") {
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
		topic := cmdarg.Fold(args[0])
		if topic == "keys" {
			return m.keys.KeyBindingHelp(), true, nil
		}
		if lines, ok := tuiHelp[topic]; ok {
			return lines, true, nil
		}
		return nil, false, nil

	case "%info":
		if len(args) == 0 || !cmdarg.Is(args[0], "edit") {
			return nil, false, nil
		}
		target := "me"
		if len(args) >= 2 {
			target = args[1]
		}
		meta := editMeta{contentType: "info", target: target}
		return nil, true, m.fetchContentCmd(meta)

	case "%memo":
		if len(args) == 0 || !cmdarg.Is(args[0], "edit") {
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
