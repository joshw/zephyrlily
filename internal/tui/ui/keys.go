package ui

import (
	"github.com/charmbracelet/bubbles/key"
)

// KeyMap defines all key bindings for the TUI.
// Implements help.KeyMap interface for bubbles/help integration.
type KeyMap struct {
	// Application control
	Quit      key.Binding
	ForceQuit key.Binding
	Suspend   key.Binding
	Redraw    key.Binding
	DebugMode key.Binding
	PasteMode key.Binding

	// Pager/viewport navigation
	PageUp       key.Binding
	PageDown     key.Binding
	HalfPageUp   key.Binding
	HalfPageDown key.Binding
	ScrollUp     key.Binding
	ScrollDown   key.Binding
	GotoTop      key.Binding
	GotoBottom   key.Binding

	// Input cursor movement
	LineStart   key.Binding
	LineEnd     key.Binding
	CharBack    key.Binding
	CharForward key.Binding
	WordBack    key.Binding
	WordForward key.Binding

	// Editing
	DeleteBack     key.Binding
	DeleteForward  key.Binding
	DeleteWord     key.Binding
	DeleteWordBack key.Binding
	KillLine       key.Binding
	KillLineBack   key.Binding
	Yank           key.Binding
	Transpose      key.Binding
	TransposeWord  key.Binding
	Capitalize     key.Binding
	Uppercase      key.Binding
	Lowercase      key.Binding

	// History and search
	HistoryPrev   key.Binding
	HistoryNext   key.Binding
	SearchBack    key.Binding
	SearchForward key.Binding

	// Actions
	Submit      key.Binding
	TabComplete key.Binding
}

// NewKeyMap returns a KeyMap with default key bindings.
func NewKeyMap() KeyMap {
	return KeyMap{
		// Application control
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("C-c", "quit"),
		),
		ForceQuit: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("C-d", "quit (empty input)"),
		),
		Suspend: key.NewBinding(
			key.WithKeys("ctrl+z"),
			key.WithHelp("C-z", "suspend"),
		),
		Redraw: key.NewBinding(
			key.WithKeys("ctrl+l"),
			key.WithHelp("C-l", "redraw"),
		),
		DebugMode: key.NewBinding(
			key.WithKeys("alt+g"),
			key.WithHelp("M-g", "toggle debug"),
		),
		PasteMode: key.NewBinding(
			key.WithKeys("alt+p"),
			key.WithHelp("M-p", "toggle paste mode"),
		),

		// Pager/viewport navigation
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "alt+v"),
			key.WithHelp("PgUp/M-v", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "ctrl+v"),
			key.WithHelp("PgDn/C-v", "page down"),
		),
		HalfPageUp: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("C-u", "half page up"),
		),
		HalfPageDown: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("C-d", "half page down"),
		),
		ScrollUp: key.NewBinding(
			key.WithKeys("alt+,"),
			key.WithHelp("M-,", "scroll up 1 line"),
		),
		ScrollDown: key.NewBinding(
			key.WithKeys("alt+."),
			key.WithHelp("M-.", "scroll down 1 line"),
		),
		GotoTop: key.NewBinding(
			key.WithKeys("alt+<"),
			key.WithHelp("M-<", "go to top"),
		),
		GotoBottom: key.NewBinding(
			key.WithKeys("alt+>"),
			key.WithHelp("M->", "go to bottom"),
		),

		// Input cursor movement
		LineStart: key.NewBinding(
			key.WithKeys("ctrl+a", "home"),
			key.WithHelp("C-a", "start of line"),
		),
		LineEnd: key.NewBinding(
			key.WithKeys("ctrl+e", "end"),
			key.WithHelp("C-e", "end of line"),
		),
		CharBack: key.NewBinding(
			key.WithKeys("ctrl+b", "left"),
			key.WithHelp("C-b/←", "back char"),
		),
		CharForward: key.NewBinding(
			key.WithKeys("ctrl+f", "right"),
			key.WithHelp("C-f/→", "forward char"),
		),
		WordBack: key.NewBinding(
			key.WithKeys("alt+b"),
			key.WithHelp("M-b", "back word"),
		),
		WordForward: key.NewBinding(
			key.WithKeys("alt+f"),
			key.WithHelp("M-f", "forward word"),
		),

		// Editing
		DeleteBack: key.NewBinding(
			key.WithKeys("backspace", "ctrl+h"),
			key.WithHelp("Bksp", "delete back"),
		),
		DeleteForward: key.NewBinding(
			key.WithKeys("delete"),
			key.WithHelp("Del", "delete forward"),
		),
		DeleteWord: key.NewBinding(
			key.WithKeys("alt+d"),
			key.WithHelp("M-d", "delete word"),
		),
		DeleteWordBack: key.NewBinding(
			key.WithKeys("ctrl+w", "alt+backspace"),
			key.WithHelp("C-w", "delete word back"),
		),
		KillLine: key.NewBinding(
			key.WithKeys("ctrl+k"),
			key.WithHelp("C-k", "kill to end"),
		),
		KillLineBack: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("C-u", "kill to start"),
		),
		Yank: key.NewBinding(
			key.WithKeys("ctrl+y"),
			key.WithHelp("C-y", "yank"),
		),
		Transpose: key.NewBinding(
			key.WithKeys("ctrl+t"),
			key.WithHelp("C-t", "transpose chars"),
		),
		TransposeWord: key.NewBinding(
			key.WithKeys("alt+t"),
			key.WithHelp("M-t", "transpose words"),
		),
		Capitalize: key.NewBinding(
			key.WithKeys("alt+c"),
			key.WithHelp("M-c", "capitalize word"),
		),
		Uppercase: key.NewBinding(
			key.WithKeys("alt+u"),
			key.WithHelp("M-u", "uppercase word"),
		),
		Lowercase: key.NewBinding(
			key.WithKeys("alt+l"),
			key.WithHelp("M-l", "lowercase word"),
		),

		// History and search
		HistoryPrev: key.NewBinding(
			key.WithKeys("ctrl+p", "up"),
			key.WithHelp("C-p/↑", "prev history"),
		),
		HistoryNext: key.NewBinding(
			key.WithKeys("ctrl+n", "down"),
			key.WithHelp("C-n/↓", "next history"),
		),
		SearchBack: key.NewBinding(
			key.WithKeys("ctrl+r"),
			key.WithHelp("C-r", "search back"),
		),
		SearchForward: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("C-s", "search forward"),
		),

		// Actions
		Submit: key.NewBinding(
			key.WithKeys("enter", "ctrl+m", "ctrl+j"),
			key.WithHelp("Enter", "send"),
		),
		TabComplete: key.NewBinding(
			key.WithKeys("tab", "ctrl+i"),
			key.WithHelp("Tab", "complete"),
		),
	}
}

// ShortHelp returns key bindings for the short help view.
// Implements help.KeyMap interface.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.Submit,
		k.PageUp,
		k.PageDown,
		k.HistoryPrev,
		k.SearchBack,
	}
}

// FullHelp returns all key bindings organized by category.
// Implements help.KeyMap interface.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		// Application
		{k.Quit, k.Redraw, k.DebugMode, k.PasteMode},
		// Navigation
		{k.PageUp, k.PageDown, k.ScrollUp, k.ScrollDown, k.GotoTop, k.GotoBottom},
		// Cursor
		{k.LineStart, k.LineEnd, k.CharBack, k.CharForward, k.WordBack, k.WordForward},
		// Editing
		{k.DeleteBack, k.DeleteWord, k.KillLine, k.KillLineBack, k.Yank, k.Transpose},
		// History/Search
		{k.HistoryPrev, k.HistoryNext, k.SearchBack, k.SearchForward},
		// Actions
		{k.Submit, k.TabComplete},
	}
}

// KeyBindingHelp returns formatted key binding help text for %help keys.
func (k KeyMap) KeyBindingHelp() []string {
	lines := []string{
		"Key Bindings",
		"",
		"Application:",
		"  C-c         quit",
		"  C-d         quit (empty input) / delete forward",
		"  C-l         redraw screen",
		"  M-g         toggle debug view",
		"  M-p         toggle paste mode",
		"",
		"Viewport Navigation:",
		"  PgUp, M-v   page up",
		"  PgDn, C-v   page down",
		"  M-<         go to top",
		"  M->         go to bottom",
		"  M-,         scroll up 1 line",
		"  M-.         scroll down 1 line",
		"",
		"Cursor Movement:",
		"  C-a, Home   start of line",
		"  C-e, End    end of line",
		"  C-b, ←      back char",
		"  C-f, →      forward char",
		"  M-b         back word",
		"  M-f         forward word",
		"",
		"Editing:",
		"  Bksp, C-h   delete back",
		"  Del         delete forward",
		"  M-d         delete word forward",
		"  C-w, M-Bksp delete word back",
		"  C-k         kill to end of line",
		"  C-u         kill to start of line",
		"  C-y         yank (paste kill buffer)",
		"  C-t         transpose chars",
		"  M-t         transpose words",
		"  M-c         capitalize word",
		"  M-u         uppercase word",
		"  M-l         lowercase word",
		"",
		"History & Search:",
		"  C-p, ↑      previous history",
		"  C-n, ↓      next history",
		"  C-r         incremental search backward",
		"  C-s         incremental search forward",
		"",
		"Completion:",
		"  Tab         complete name / cycle recent",
		"  ;           expand and add separator",
		"  :           expand (for emote)",
		"  ,           expand, continue list",
		"  =           recall sendgroup",
		"",
		"Actions:",
		"  Enter       send message / page down (empty)",
	}
	return lines
}
