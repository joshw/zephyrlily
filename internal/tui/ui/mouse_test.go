package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMouseModel builds a Model suitable for exercising the %mouse command.
func newMouseModel(t *testing.T) Model {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	// Leave auth mode: View() only declares a mouse mode in the normal UI.
	m.authMode = false
	return m
}

// lastOutputLines returns the rendered lines of the most recent output item.
func lastOutputLines(t *testing.T, m Model) []string {
	t.Helper()
	require.NotEmpty(t, m.output, "expected at least one output item")
	lines, ok := m.output[len(m.output)-1].Data.([]string)
	require.True(t, ok, "last output item Data is not []string")
	return lines
}

func TestMouseToggle(t *testing.T) {
	m := newMouseModel(t)
	require.False(t, m.mouseEnabled, "mouse mode should be off by default")

	// Turn it on: state flips, the View declares cell-motion mouse mode
	// (bubbletea v2 has no enable/disable commands), and the copy/paste
	// notice is printed.
	m, _ = m.submitLine("%mouse on")
	assert.True(t, m.mouseEnabled, "%mouse on should enable mouse mode")
	assert.Equal(t, tea.MouseModeCellMotion, m.View().MouseMode,
		"enabling should declare cell-motion mouse mode in the View")

	on := strings.Join(lastOutputLines(t, m), "\n")
	assert.Contains(t, on, "Mouse mode: on")
	assert.Contains(t, on, "text selection", "enabling should explain the selection tradeoff")
	for _, frag := range []string{"Shift", "Option", "Fn"} {
		assert.Containsf(t, on, frag, "notice should mention the %s bypass modifier", frag)
	}
	assert.Contains(t, on, "M-m", "notice should point at the key toggle")
	assert.Contains(t, on, "zlilyStartup", "notice should mention making it the default")

	// Turn it off: state flips back and the View stops declaring a mouse mode,
	// with no notice this time.
	m, _ = m.submitLine("%mouse off")
	assert.False(t, m.mouseEnabled, "%mouse off should disable mouse mode")
	assert.Equal(t, tea.MouseModeNone, m.View().MouseMode,
		"disabling should declare no mouse mode in the View")
	off := lastOutputLines(t, m)
	require.Len(t, off, 1, "turning off should be a single line, not the full notice")
	assert.Contains(t, off[0], "Mouse mode: off")
}

func TestMouseQuery(t *testing.T) {
	m := newMouseModel(t)

	// Bare query reflects state without changing it or issuing a command.
	m, cmd := m.submitLine("%mouse")
	assert.False(t, m.mouseEnabled, "query should not change state")
	assert.Nil(t, cmd, "query should not issue a command")
	assert.Equal(t, []string{"Mouse mode: off"}, lastOutputLines(t, m))

	m.mouseEnabled = true
	m, _ = m.submitLine("%mouse")
	assert.Equal(t, []string{"Mouse mode: on"}, lastOutputLines(t, m))
}

func TestMouseUsage(t *testing.T) {
	m := newMouseModel(t)
	m, cmd := m.submitLine("%mouse bogus")
	assert.Nil(t, cmd, "bad argument should not issue a command")
	assert.False(t, m.mouseEnabled, "bad argument should not change state")
	assert.Equal(t, []string{"Usage: %mouse on|off"}, lastOutputLines(t, m))
}

// %mouse has to survive the clientcommand path, because that is how a
// zlilyStartup memo line reaches the client.
func TestMouseFromStartupMemoPath(t *testing.T) {
	m := newMouseModel(t)
	m, out, _, recognized := m.applyLocalCommand("%mouse on")
	assert.True(t, recognized, "%mouse must be recognized as a local command")
	assert.True(t, m.mouseEnabled)
	assert.Contains(t, strings.Join(out, "\n"), "Mouse mode: on")
}

// TestPageToggleUnaffected guards that the plain pager toggle still works after
// the wheel subcommand moved out to %mouse.
func TestPageToggleUnaffected(t *testing.T) {
	m := newMouseModel(t)
	require.True(t, m.pagerEnabled, "pager is on by default")

	m, _ = m.submitLine("%page off")
	assert.False(t, m.pagerEnabled)
	assert.Equal(t, []string{"Viewport pager: off"}, lastOutputLines(t, m))

	m, _ = m.submitLine("%page on")
	assert.True(t, m.pagerEnabled)
	assert.Equal(t, []string{"Viewport pager: on"}, lastOutputLines(t, m))
}

// pressAltM sends M-m through the normal key path.
func pressAltM(t *testing.T, m Model) Model {
	t.Helper()
	// No Text: a real alt chord carries none, and String() prefers Text when
	// it is set (see the meta-prefix handling in handleNormalKey).
	upd, _ := m.Update(tea.KeyPressMsg{Code: 'm', Mod: tea.ModAlt})
	return upd.(Model)
}

func TestMouseKeyToggle(t *testing.T) {
	m := newMouseModel(t)
	m = sizeTo(t, m, 80, 24)
	require.False(t, m.mouseEnabled)

	m = pressAltM(t, m)
	assert.True(t, m.mouseEnabled, "M-m should turn mouse mode on")
	assert.Equal(t, tea.MouseModeCellMotion, m.View().MouseMode)
	on := lastOutputLines(t, m)
	require.Len(t, on, 1, "the key toggle should stay terse")
	assert.Contains(t, on[0], "Mouse mode: on")
	assert.Contains(t, on[0], "M-m")

	m = pressAltM(t, m)
	assert.False(t, m.mouseEnabled, "M-m should turn mouse mode back off")
	assert.Equal(t, tea.MouseModeNone, m.View().MouseMode)
	assert.Contains(t, lastOutputLines(t, m)[0], "Mouse mode: off")
}

// M-m is the escape hatch for grabbing a selection, so it must work with text
// already half-typed without disturbing it.
func TestMouseKeyToggleLeavesInputAlone(t *testing.T) {
	m := newMouseModel(t)
	m = sizeTo(t, m, 80, 24)
	m.inputValue = "half typed line"
	m.inputCursor = 4
	m.syncTextarea()

	m = pressAltM(t, m)
	assert.True(t, m.mouseEnabled)
	assert.Equal(t, "half typed line", m.inputValue, "toggling must not touch the input")
	assert.Equal(t, 4, m.inputCursor, "toggling must not move the cursor")
}

func TestStatusBarMouseIndicator(t *testing.T) {
	m := newMouseModel(t)
	m = sizeTo(t, m, 100, 24)
	m.state = &api.StateResponse{
		Server:   "lily.example",
		Whoami:   "#1",
		Entities: []api.EntityJSON{{Handle: "#1", Kind: "user", Name: "Josh", State: "here"}},
	}

	off := ansi.Strip(m.formatStatusBar())
	assert.NotContains(t, off, "[M]", "no indicator while mouse mode is off")

	m.mouseEnabled = true
	on := ansi.Strip(m.formatStatusBar())
	require.Contains(t, on, "[M]", "mouse mode should be flagged in the status bar")

	// It sits immediately left of the clock, separated the same way as the
	// other fields.
	assert.Regexp(t, `here \| \[M\] \| \d\d:\d\d`, on,
		"indicator should be between the user state and the time, pipe-separated")
}
