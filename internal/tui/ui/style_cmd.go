package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// styleDescriptor tracks a user-editable style: the live lipgloss vars it drives
// (more than one when vars are aliased, e.g. header + timestamp), a snapshot of
// the compiled-in default, and the display tokens for the current/default
// foreground and background (so the table can show "red" rather than "#FF0000").
type styleDescriptor struct {
	name string
	ptrs []*lipgloss.Style

	def          lipgloss.Style
	defFgTok     string
	defBgTok     string
	defBold      bool
	defUnderline bool

	fgTok string
	bgTok string
}

// styleList is the ordered registry of editable styles. Order is fixed so the
// %style table lists consistently. Entries with multiple pointers keep aliased
// vars in sync (header and its timestamp copy).
var styleList = []*styleDescriptor{
	{name: "public-sender", ptrs: []*lipgloss.Style{&publicSenderStyle}},
	{name: "public-header", ptrs: []*lipgloss.Style{&publicHeaderStyle, &publicTimestampStyle}},
	{name: "public-body", ptrs: []*lipgloss.Style{&publicBodyStyle}},
	{name: "public-blurb", ptrs: []*lipgloss.Style{&publicBlurbStyle}},
	{name: "private-sender", ptrs: []*lipgloss.Style{&privateSenderStyle}},
	{name: "private-header", ptrs: []*lipgloss.Style{&privateHeaderStyle, &privateTimestampStyle}},
	{name: "private-body", ptrs: []*lipgloss.Style{&privateBodyStyle}},
	{name: "private-blurb", ptrs: []*lipgloss.Style{&privateBlurbStyle}},
	{name: "emote", ptrs: []*lipgloss.Style{&emoteBodyStyle}},
	{name: "slcp-body", ptrs: []*lipgloss.Style{&slcpBodyStyle}},
	{name: "command", ptrs: []*lipgloss.Style{&commandResultStyle}},
	{name: "error", ptrs: []*lipgloss.Style{&errorStyle}},
	{name: "prompt", ptrs: []*lipgloss.Style{&promptStyle}},
	{name: "input", ptrs: []*lipgloss.Style{&inputStyle}},
	{name: "status-bar", ptrs: []*lipgloss.Style{&statusBarStyle}},
	{name: "log-info", ptrs: []*lipgloss.Style{&logInfoSeverityStyle}},
	{name: "log-error", ptrs: []*lipgloss.Style{&logErrorSeverityStyle}},
	{name: "log-prefix", ptrs: []*lipgloss.Style{&logPrefixStyle}},
	{name: "misspelled", ptrs: []*lipgloss.Style{&misspelledStyle}},
	{name: "cursor", ptrs: []*lipgloss.Style{&cursorStyle}},
}

var styleByName = map[string]*styleDescriptor{}

// colorNames maps friendly color names to ANSI color codes (as strings).
var colorNames = map[string]string{
	"black": "0", "red": "1", "green": "2", "yellow": "3",
	"blue": "4", "magenta": "5", "cyan": "6", "white": "7",
	"gray": "8", "grey": "8", "brightblack": "8",
	"brightred": "9", "brightgreen": "10", "brightyellow": "11",
	"brightblue": "12", "brightmagenta": "13", "brightcyan": "14",
	"brightwhite": "15",
}

// reverseNames maps ANSI color codes back to a preferred display name.
var reverseNames = map[string]string{
	"0": "black", "1": "red", "2": "green", "3": "yellow",
	"4": "blue", "5": "magenta", "6": "cyan", "7": "white",
	"8": "gray", "9": "brightred", "10": "brightgreen", "11": "brightyellow",
	"12": "brightblue", "13": "brightmagenta", "14": "brightcyan", "15": "brightwhite",
}

func init() {
	for _, d := range styleList {
		d.def = *d.ptrs[0]
		d.defFgTok = tokenFromColor(d.def.GetForeground())
		d.defBgTok = tokenFromColor(d.def.GetBackground())
		d.defBold = d.def.GetBold()
		d.defUnderline = d.def.GetUnderline()
		d.fgTok = d.defFgTok
		d.bgTok = d.defBgTok
		styleByName[d.name] = d
	}
}

// tokenFromColor produces a display token for a terminal color: a friendly name
// for ANSI 0-15, the raw value for hex/256-color, or "none" when unset.
func tokenFromColor(tc lipgloss.TerminalColor) string {
	c, ok := tc.(lipgloss.Color)
	if !ok {
		return "none"
	}
	s := string(c)
	if s == "" {
		return "none"
	}
	if name, ok := reverseNames[s]; ok {
		return name
	}
	return s
}

// parseColor resolves a user color token. unset is true for "none"; ok is false
// for an unrecognized token (the caller should report an error and not change
// anything). "default" is handled by the command layer, not here.
func parseColor(tok string) (color lipgloss.Color, unset bool, ok bool) {
	tok = strings.ToLower(strings.TrimSpace(tok))
	if tok == "none" {
		return "", true, true
	}
	if strings.HasPrefix(tok, "#") {
		return lipgloss.Color(tok), false, true
	}
	if n, err := strconv.Atoi(tok); err == nil {
		if n < 0 || n > 255 {
			return "", false, false
		}
		return lipgloss.Color(tok), false, true
	}
	if code, found := colorNames[tok]; found {
		return lipgloss.Color(code), false, true
	}
	return "", false, false
}

// canonicalToken normalizes a user-entered color token to how it will be stored
// and displayed (e.g. "RED" -> "red", an ANSI number -> its name).
func canonicalToken(tok string) string {
	tok = strings.ToLower(strings.TrimSpace(tok))
	if tok == "none" {
		return "none"
	}
	if strings.HasPrefix(tok, "#") {
		return tok
	}
	if name, ok := reverseNames[tok]; ok {
		return name
	}
	if code, ok := colorNames[tok]; ok {
		return reverseNames[code]
	}
	return tok
}

// each applies fn to every live pointer of a descriptor.
func (d *styleDescriptor) each(fn func(lipgloss.Style) lipgloss.Style) {
	for _, p := range d.ptrs {
		*p = fn(*p)
	}
}

func (d *styleDescriptor) setFg(tok string) bool {
	c, unset, ok := parseColor(tok)
	if !ok {
		return false
	}
	d.each(func(s lipgloss.Style) lipgloss.Style {
		if unset {
			return s.UnsetForeground()
		}
		return s.Foreground(c)
	})
	d.fgTok = canonicalToken(tok)
	return true
}

func (d *styleDescriptor) setBg(tok string) bool {
	c, unset, ok := parseColor(tok)
	if !ok {
		return false
	}
	d.each(func(s lipgloss.Style) lipgloss.Style {
		if unset {
			return s.UnsetBackground()
		}
		return s.Background(c)
	})
	d.bgTok = canonicalToken(tok)
	return true
}

func (d *styleDescriptor) setBold(on bool) {
	d.each(func(s lipgloss.Style) lipgloss.Style { return s.Bold(on) })
}

func (d *styleDescriptor) setUnderline(on bool) {
	d.each(func(s lipgloss.Style) lipgloss.Style { return s.Underline(on) })
}

// resetAttr restores a single attribute ("fg" or "bg") to the default.
func (d *styleDescriptor) resetAttr(which string) {
	switch which {
	case "fg":
		def := d.def.GetForeground()
		d.each(func(s lipgloss.Style) lipgloss.Style {
			if _, ok := def.(lipgloss.Color); !ok {
				return s.UnsetForeground()
			}
			return s.Foreground(def)
		})
		d.fgTok = d.defFgTok
	case "bg":
		def := d.def.GetBackground()
		d.each(func(s lipgloss.Style) lipgloss.Style {
			if _, ok := def.(lipgloss.Color); !ok {
				return s.UnsetBackground()
			}
			return s.Background(def)
		})
		d.bgTok = d.defBgTok
	}
}

// resetStyle restores the whole style to its compiled-in default.
func (d *styleDescriptor) resetStyle() {
	d.each(func(lipgloss.Style) lipgloss.Style { return d.def })
	d.fgTok = d.defFgTok
	d.bgTok = d.defBgTok
}

// clearStyle removes all styling (unstyled but visible).
func (d *styleDescriptor) clearStyle() {
	d.each(func(lipgloss.Style) lipgloss.Style { return lipgloss.NewStyle() })
	d.fgTok = "none"
	d.bgTok = "none"
}

func (d *styleDescriptor) bold() bool      { return d.ptrs[0].GetBold() }
func (d *styleDescriptor) underline() bool { return d.ptrs[0].GetUnderline() }

func (d *styleDescriptor) modified() bool {
	return d.fgTok != d.defFgTok || d.bgTok != d.defBgTok ||
		d.bold() != d.defBold || d.underline() != d.defUnderline
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// styleNames returns the editable style names, sorted, for error messages.
func styleNames() []string {
	names := make([]string, 0, len(styleList))
	for _, d := range styleList {
		names = append(names, d.name)
	}
	sort.Strings(names)
	return names
}

// styleRow formats one table row. The swatch is placed last so that the ANSI
// escape sequences it contains don't disturb column alignment.
func styleRow(d *styleDescriptor) string {
	def := "yes"
	if d.modified() {
		def = "no"
	}
	swatch := (*d.ptrs[0]).Render("sample")
	return fmt.Sprintf("%-15s %-12s %-12s %-5s %-5s %-8s %s",
		d.name, d.fgTok, d.bgTok, onOff(d.bold()), onOff(d.underline()), def, swatch)
}

func styleHeader() string {
	return fmt.Sprintf("%-15s %-12s %-12s %-5s %-5s %-8s %s",
		"NAME", "FG", "BG", "BOLD", "UNDR", "DEFAULT?", "SAMPLE")
}

func renderStyleTable() []string {
	out := []string{styleHeader()}
	for _, d := range styleList {
		out = append(out, styleRow(d))
	}
	out = append(out, "", "Use '%help style' for usage.")
	return out
}

// handleStyleCommand implements the %style command. It mutates the package-level
// style vars in place; the caller re-renders the viewport so changes show up
// immediately. Returns the lines to display.
func handleStyleCommand(args []string) []string {
	usage := []string{"Usage: %style [list] | %style <name> [fg|bg <color>|bold|underline on|off|default|none] | %style all default"}

	if len(args) == 0 || args[0] == "list" {
		return renderStyleTable()
	}

	// %style all default
	if args[0] == "all" {
		if len(args) == 2 && args[1] == "default" {
			for _, d := range styleList {
				d.resetStyle()
			}
			return []string{"All styles reset to defaults."}
		}
		return usage
	}

	name := args[0]
	d, ok := styleByName[name]
	if !ok {
		return append([]string{"Unknown style: " + name, "Valid styles:"},
			"  "+strings.Join(styleNames(), ", "))
	}

	// %style <name> -> show just that style
	if len(args) == 1 {
		return []string{styleHeader(), styleRow(d)}
	}

	switch args[1] {
	case "default":
		d.resetStyle()
		return []string{name + " reset to default."}
	case "none":
		d.clearStyle()
		return []string{name + " cleared (unstyled)."}
	case "fg", "bg":
		if len(args) != 3 {
			return usage
		}
		tok := args[2]
		if tok == "default" {
			d.resetAttr(args[1])
			return []string{fmt.Sprintf("%s %s reset to default.", name, args[1])}
		}
		var okSet bool
		if args[1] == "fg" {
			okSet = d.setFg(tok)
		} else {
			okSet = d.setBg(tok)
		}
		if !okSet {
			return []string{"Invalid color: " + tok,
				"Use 0-255, a name (red, cyan, brightyellow, ...), #rrggbb, or none."}
		}
		return []string{fmt.Sprintf("%s %s set to %s.", name, args[1], canonicalToken(tok))}
	case "bold", "underline":
		if len(args) != 3 {
			return usage
		}
		var on bool
		switch strings.ToLower(args[2]) {
		case "on":
			on = true
		case "off":
			on = false
		default:
			return []string{"Usage: %style " + name + " " + args[1] + " on|off"}
		}
		if args[1] == "bold" {
			d.setBold(on)
		} else {
			d.setUnderline(on)
		}
		return []string{fmt.Sprintf("%s %s %s.", name, args[1], onOff(on))}
	}

	return usage
}
