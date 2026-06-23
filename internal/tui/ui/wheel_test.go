package ui

import (
	"strings"
	"testing"

	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWheelModel builds a Model suitable for exercising the %page wheel command.
func newWheelModel(t *testing.T) Model {
	t.Helper()
	logChan, _ := NewLogger()
	return New(client.New(""), logChan)
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

	// Turn it on: state flips, a runtime command is issued to enable mouse
	// reporting, and the selection warning is printed.
	m, cmd := m.submitLine("%page wheel on")
	assert.True(t, m.mouseWheel, "%page wheel on should enable wheel scrolling")
	assert.NotNil(t, cmd, "enabling should issue a mouse-enable command")
	on := lastOutputLines(t, m)
	assert.Equal(t, "Mouse-wheel scrolling: on", on[0])
	assert.Contains(t, strings.Join(on, "\n"), "text selection",
		"enabling should warn about broken text selection")
	for _, frag := range []string{"Shift", "Option", "Fn"} {
		assert.Contains(t, strings.Join(on, "\n"), frag,
			"warning should mention the %s bypass modifier", frag)
	}

	// Turn it off: state flips back and a disable command is issued, with no
	// warning this time.
	m, cmd = m.submitLine("%page wheel off")
	assert.False(t, m.mouseWheel, "%page wheel off should disable wheel scrolling")
	assert.NotNil(t, cmd, "disabling should issue a mouse-disable command")
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
