package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joshw/zephyrlily/internal/tui/client"
)

// TestInputAreaKeepsOffLastColumn pins the mosh workaround described at
// reservedColumns: the bottom row of the frame must never reach the terminal's
// last column, because writing there leaves the pending-wrap state that mosh
// 1.4.0 mishandles in two separate ways (see display-bug-repro/upstream).
//
// The bottom row is the one that matters. Mosh re-anchors every other row with
// a CR/LF or an absolute cursor move, so a pending wrap higher up the screen is
// discarded before anything depends on it; only the last row it paints can
// carry that state into the next frame. That is the input area, which is why
// the reservation lives there and not on the full-width status bar.
func TestInputAreaKeepsOffLastColumn(t *testing.T) {
	logChan, _ := NewLogger()

	for _, w := range []int{20, 40, 80, 81} {
		base := New(client.New(""), logChan)
		base.authMode = false
		base.reserveLastColumn = true
		base = sizeTo(t, base, w, 12)

		// Sweep well past the first wrap so both the first line (which pays for
		// the prompt) and the continuation lines are covered.
		for n := 1; n <= w*2+2; n++ {
			m := base
			m.inputValue = strings.Repeat("a", n)
			m.inputCursor = n
			m = m.maybeResizeViewport()

			lines := strings.Split(m.viewContent(), "\n")
			last := lines[len(lines)-1]
			require.Lessf(t, ansi.StringWidth(last), w,
				"width=%d, %d chars: bottom row reaches the last column, "+
					"leaving the pending wrap that trips mosh: %q", w, n, last)
		}
	}
}

// TestEveryModeKeepsOffLastColumn extends the invariant past the normal view.
// Whichever mode is on screen owns the bottom row, so each has to respect the
// reservation: the editor's footer was full-width and did paint the last
// column, which is exactly the exposure this test exists to catch.
func TestEveryModeKeepsOffLastColumn(t *testing.T) {
	logChan, _ := NewLogger()
	const w, h = 80, 24

	modes := map[string]func(Model) Model{
		"normal": func(m Model) Model { return m },
		"debug":  func(m Model) Model { m.debugMode = true; return m },
		"editor": func(m Model) Model {
			m.editMode = true
			m.editor = newEditorModel(m.width, m.height-2, strings.Repeat("e", w*3))
			return m
		},
		"auth": func(m Model) Model { m.authMode = true; return m },
	}

	for name, setup := range modes {
		t.Run(name, func(t *testing.T) {
			m := New(client.New(""), logChan)
			m.authMode = false
			m.reserveLastColumn = true
			m = sizeTo(t, m, w, h)
			m.inputValue = strings.Repeat("a", w+w/2)
			m.inputCursor = len(m.inputValue)
			m = setup(m.maybeResizeViewport())

			lines := strings.Split(m.viewContent(), "\n")
			last := lines[len(lines)-1]
			assert.Lessf(t, ansi.StringWidth(last), w,
				"%s mode paints the last column of the bottom row: %q", name, last)
		})
	}
}

// TestReserveLastColumnOffLetsInputReachTheEdge is the control for the test
// above: with the workaround disabled the input does reach the last column, so
// we know that assertion is measuring the reservation and not some unrelated
// property of the renderer that would hold either way.
func TestReserveLastColumnOffLetsInputReachTheEdge(t *testing.T) {
	logChan, _ := NewLogger()
	const w = 40

	m := New(client.New(""), logChan)
	m.authMode = false
	m = sizeTo(t, m, w, 12)
	require.False(t, m.reserveLastColumn, "off is the default")

	// One short of the cursor cell filling the last column.
	n := m.inputFirstLineWidth() - 1
	m.inputValue = strings.Repeat("a", n)
	m.inputCursor = n
	m = m.maybeResizeViewport()

	lines := strings.Split(m.viewContent(), "\n")
	assert.Equal(t, w, ansi.StringWidth(lines[len(lines)-1]),
		"with the reservation off, the input should fill the row")
}

// TestDebugLastColToggle covers the %debug lastcol command, which exists so a
// user hitting the mosh bugs can turn the workaround off and confirm it is what
// is holding them back.
func TestDebugLastColToggle(t *testing.T) {
	logChan, _ := NewLogger()
	base := New(client.New(""), logChan)
	base.authMode = false
	base = sizeTo(t, base, 80, 24)

	require.False(t, base.reserveLastColumn, "the workaround is off by default")

	m, out, _ := base.handleDebugCommand([]string{"debug", "lastcol"})
	assert.Equal(t, []string{"Reserve last column: off"}, out)
	assert.False(t, m.reserveLastColumn, "a bare query must not change the setting")
	assert.Equal(t, m.width, m.inputWrapWidth(), "off gives the input the full width")

	m, out, _ = m.handleDebugCommand([]string{"debug", "lastcol", "on"})
	assert.Equal(t, []string{"Reserve last column: on"}, out)
	assert.True(t, m.reserveLastColumn)
	assert.Equal(t, m.width-1, m.inputWrapWidth(), "on holds a column back")

	m, out, _ = m.handleDebugCommand([]string{"debug", "lastcol", "off"})
	assert.Equal(t, []string{"Reserve last column: off"}, out)
	assert.False(t, m.reserveLastColumn)

	_, out, _ = m.handleDebugCommand([]string{"debug", "lastcol", "sideways"})
	assert.Equal(t, []string{"Usage: %debug lastcol on|off"}, out)
}
