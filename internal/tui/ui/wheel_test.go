package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWheelModel builds a Model suitable for exercising the %page wheel command.
func newWheelModel(t *testing.T) Model {
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

func TestPageWheelToggle(t *testing.T) {
	m := newWheelModel(t)
	require.False(t, m.mouseWheel, "wheel scrolling should be off by default")

	// Turn it on: state flips, the View declares cell-motion mouse mode
	// (bubbletea v2 has no enable/disable commands), and the selection
	// warning is printed.
	m, _ = m.submitLine("%page wheel on")
	assert.True(t, m.mouseWheel, "%page wheel on should enable wheel scrolling")
	assert.Equal(t, tea.MouseModeCellMotion, m.View().MouseMode,
		"enabling should declare cell-motion mouse mode in the View")
	on := lastOutputLines(t, m)
	assert.Equal(t, "Mouse-wheel scrolling: on", on[0])
	assert.Contains(t, strings.Join(on, "\n"), "text selection",
		"enabling should warn about broken text selection")
	for _, frag := range []string{"Shift", "Option", "Fn"} {
		assert.Contains(t, strings.Join(on, "\n"), frag,
			"warning should mention the %s bypass modifier", frag)
	}

	// Turn it off: state flips back and the View stops declaring a mouse
	// mode, with no warning this time.
	m, _ = m.submitLine("%page wheel off")
	assert.False(t, m.mouseWheel, "%page wheel off should disable wheel scrolling")
	assert.Equal(t, tea.MouseModeNone, m.View().MouseMode,
		"disabling should declare no mouse mode in the View")
	off := lastOutputLines(t, m)
	assert.Equal(t, []string{"Mouse-wheel scrolling: off"}, off)
}

func TestPageWheelQuery(t *testing.T) {
	m := newWheelModel(t)

	// Bare query reflects state without changing it or issuing a command.
	m, cmd := m.submitLine("%page wheel")
	assert.False(t, m.mouseWheel, "query should not change state")
	assert.Nil(t, cmd, "query should not issue a command")
	assert.Equal(t, []string{"Mouse-wheel scrolling: off"}, lastOutputLines(t, m))

	m.mouseWheel = true
	m, _ = m.submitLine("%page wheel")
	assert.Equal(t, []string{"Mouse-wheel scrolling: on"}, lastOutputLines(t, m))
}

func TestPageWheelUsage(t *testing.T) {
	m := newWheelModel(t)
	m, cmd := m.submitLine("%page wheel bogus")
	assert.Nil(t, cmd, "bad argument should not issue a command")
	assert.False(t, m.mouseWheel, "bad argument should not change state")
	assert.Equal(t, []string{"Usage: %page wheel on|off"}, lastOutputLines(t, m))
}

// TestPageToggleUnaffected guards that the plain pager toggle still works
// alongside the new wheel subcommand.
func TestPageToggleUnaffected(t *testing.T) {
	m := newWheelModel(t)
	require.True(t, m.pagerEnabled, "pager is on by default")

	m, _ = m.submitLine("%page off")
	assert.False(t, m.pagerEnabled)
	assert.Equal(t, []string{"Viewport pager: off"}, lastOutputLines(t, m))

	m, _ = m.submitLine("%page on")
	assert.True(t, m.pagerEnabled)
	assert.Equal(t, []string{"Viewport pager: on"}, lastOutputLines(t, m))
}
