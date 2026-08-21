package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The TUI's local commands match their names and their fixed keyword arguments
// case-insensitively, while free-form arguments keep their case. See
// internal/cmdarg.

// TestMouseCommandIgnoresCase covers the reported expectation directly: every
// casing of "%mouse on" must behave identically.
func TestMouseCommandIgnoresCase(t *testing.T) {
	for _, line := range []string{"%mouse on", "%mouse ON", "%MOUSE on", "%Mouse On", "%MOUSE ON"} {
		m := newMouseModel(t)
		m, out, _, handled := m.applyLocalCommand(line)
		require.True(t, handled, "%s should be recognised as a local command", line)
		assert.True(t, m.mouseEnabled, "%s should enable mouse mode", line)
		assert.NotContains(t, strings.Join(out, "\n"), "Usage:", "%s printed the usage line", line)
	}

	for _, line := range []string{"%mouse off", "%MOUSE OFF", "%Mouse Off"} {
		m := newMouseModel(t)
		m.mouseEnabled = true
		m, _, _, handled := m.applyLocalCommand(line)
		require.True(t, handled, "%s should be recognised as a local command", line)
		assert.False(t, m.mouseEnabled, "%s should disable mouse mode", line)
	}

	// Only on|off are accepted, in any case — not other spellings of "true".
	m := newMouseModel(t)
	_, out, _, handled := m.applyLocalCommand("%MOUSE TRUE")
	require.True(t, handled)
	assert.Equal(t, []string{"Usage: %mouse on|off"}, out)
}

func TestPageCommandIgnoresCase(t *testing.T) {
	// %page's name was the one case-sensitive holdout among its neighbours.
	for _, line := range []string{"%page off", "%PAGE off", "%Page OFF"} {
		m := newMouseModel(t)
		m.pagerEnabled = true
		m, out, _, handled := m.applyLocalCommand(line)
		require.True(t, handled, "%s should be recognised as a local command", line)
		assert.False(t, m.pagerEnabled, "%s should disable the pager", line)
		assert.Equal(t, []string{"Viewport pager: off"}, out)
	}

	m := newMouseModel(t)
	m, _, _, handled := m.applyLocalCommand("%PAGE ON")
	require.True(t, handled)
	assert.True(t, m.pagerEnabled)
}

func TestSpellCommandIgnoresCase(t *testing.T) {
	s := NewSpellChecker()

	if got := s.HandleCommand([]string{"OFF"}); len(got) == 0 || s.Enabled() {
		t.Errorf("%%spell OFF did not disable; out=%v enabled=%v", got, s.Enabled())
	}
	if got := s.HandleCommand([]string{"On"}); len(got) == 0 || !s.Enabled() {
		t.Errorf("%%spell On did not enable; out=%v enabled=%v", got, s.Enabled())
	}

	// The subcommand folds; the words after it are echoed back verbatim.
	out := s.HandleCommand([]string{"ALLOW", "Zlily"})
	if len(out) != 1 || !strings.Contains(out[0], `"Zlily"`) {
		t.Errorf("ALLOW = %v, want the word echoed with its original case", out)
	}
	if !s.CheckWord("Zlily") {
		t.Error("ALLOW did not allow the word")
	}
}

func TestStyleCommandIgnoresCase(t *testing.T) {
	// Style names are a closed set, so they fold too; the canonical spelling is
	// echoed back.
	out := handleStyleCommand([]string{"ERROR", "BOLD", "ON"})
	require.Len(t, out, 1)
	assert.Equal(t, "error bold on.", out[0])

	out = handleStyleCommand([]string{"Error", "Fg", "RED"})
	require.Len(t, out, 1)
	assert.Equal(t, "error fg set to red.", out[0])

	out = handleStyleCommand([]string{"error", "FG", "DEFAULT"})
	require.Len(t, out, 1)
	assert.Equal(t, "error fg reset to default.", out[0])

	// An unknown style still reports the name as the user spelled it.
	out = handleStyleCommand([]string{"NoSuchStyle"})
	require.NotEmpty(t, out)
	assert.Contains(t, out[0], "NoSuchStyle")

	handleStyleCommand([]string{"ALL", "DEFAULT"})
}

func TestLocalCommandNamesIgnoreCase(t *testing.T) {
	m := newMouseModel(t)

	// %help and its topics.
	lower, handledLower, _ := m.handleLocalCommand("%help snapshot")
	upper, handledUpper, _ := m.handleLocalCommand("%HELP SNAPSHOT")
	require.True(t, handledLower, "%help snapshot should be handled")
	assert.Equal(t, handledLower, handledUpper)
	assert.Equal(t, lower, upper)

	// %debug snapshot's subcommand folds; a bad one still prints usage.
	_, out, _, handled := m.applyLocalCommand("%DEBUG NOPE")
	require.True(t, handled)
	assert.Equal(t, debugUsage, out)

	// The Lily-command interceptions fold their subcommand.
	out, handled, _ = m.handleLocalCommand("/INFO SET")
	assert.True(t, handled, "/INFO SET should be intercepted")
	assert.Equal(t, []string{"Use %info edit [target] to edit your info."}, out)

	out, handled, _ = m.handleLocalCommand("/Memo Set")
	assert.True(t, handled, "/Memo Set should be intercepted")
	assert.Equal(t, []string{"Use %memo edit [target] <name> to edit a memo."}, out)

	// %memo edit's target and name are free-form and keep their case.
	out, handled, _ = m.handleLocalCommand("%MEMO EDIT")
	assert.True(t, handled)
	assert.Equal(t, []string{"Usage: %memo edit [target] <name>"}, out)
}
