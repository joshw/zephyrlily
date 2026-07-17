package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/joshw/zephyrlily/internal/tui/client"
)

const longURL = "https://scontent-iad3-2.xx.fbcdn.net/v/t39.99422-6/747038350_28117976701121955_6928995500826763906_n.png?stp=dst-jpg_tt6&cstp=mx1608x2144&ctp=s1608x2144&_nc_cat=111&ccb=1-7&_nc_sid=127cfc&_nc_ohc=oiY1F8iplg0Q7kNvwGrIAw2&_nc_oc=AdoYDc8I_MX_-BG47lLqAfZqPRznZUogSJV_Dgw0brTaF8y2op1ASwfakvaHHC_bp8w&_nc_zt=14&_nc_ht=scontent-iad3-2.xx&_nc_gid=6xlzlcQ0YnjRAovPvca_sQ&_nc_ss=7b2a8&oh=00_AQAJBHfZkYfhJIq6dXiDDhocTXSm2vrVKi7y35IW2fzAxA&oe=6A5DCE67"

// terminalWidth computes the number of display cells a terminal would use for
// s, using the project's own escSeqLen as ground truth (CSI + OSC aware).
// Non-escape content is measured per rune (the status bar pads with U+00A0,
// which is one cell but two bytes).
func terminalWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if n := escSeqLen(s, i); n > 0 {
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w += ansi.StringWidth(string(r))
		i += size
	}
	return w
}

// unterminatedOSC reports whether s contains an OSC sequence that is never
// terminated by BEL or ST.
func unterminatedOSC(s string) bool {
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == ']' {
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 {
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					break
				}
				j++
			}
			if j >= len(s) {
				return true
			}
			i = j
			continue
		}
		i++
	}
	return false
}

func checkLines(t *testing.T, label string, lines []string, width int) {
	t.Helper()
	for i, line := range lines {
		tw := terminalWidth(line)
		if tw > width {
			t.Errorf("%s line %d: terminal width %d exceeds %d\n%q", label, i, tw, width, line)
		}
		if unterminatedOSC(line) {
			t.Errorf("%s line %d has unterminated OSC:\n%q", label, i, line)
		}
		trunc := ansi.Truncate(line, width, "")
		if tw2 := terminalWidth(trunc); tw2 > width {
			t.Errorf("%s line %d after bubbletea Truncate: width %d > %d", label, i, tw2, width)
		}
		if unterminatedOSC(trunc) {
			t.Errorf("%s line %d after bubbletea Truncate has unterminated OSC:\n%q", label, i, trunc)
		}
	}
}

func TestLongURLFullView(t *testing.T) {
	// lipgloss v2 renders colors as specified regardless of environment
	// (profile downgrading moved into the program renderer), so the v1-era
	// global SetColorProfile(TrueColor) pin is no longer needed.
	values := map[string]string{
		"leading-space": " says check this out " + longURL + " pretty wild",
		"url-only":      " " + longURL,
		"possessive":    "'s favorite: " + longURL,
	}

	for name, value := range values {
		for _, width := range []int{40, 80, 81, 120, 137, 203} {
			height := 24
			logChan, _ := NewLogger()
			m := New(client.New(""), logChan)
			m.authMode = false
			m.output = []OutputItem{
				{Type: "text", Data: "hello world"},
				{Type: "event", Data: emoteEvent("#3", "carol", "#5", "cafe", value)},
			}
			m = sizeTo(t, m, width, height)

			view := m.viewContent()
			vlines := strings.Split(view, "\n")
			if len(vlines) != height {
				t.Errorf("%s w=%d: View has %d lines, want %d", name, width, len(vlines), height)
			}
			checkLines(t, name+" formatEvent",
				strings.Split(formatEvent(emoteEvent("#3", "carol", "#5", "cafe", value), width, "someone"), "\n"), width)
			checkLines(t, name+" view", vlines, width)
		}
	}
}
