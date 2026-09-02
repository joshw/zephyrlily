package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joshw/zephyrlily/internal/tui/client"
)

func TestMoshServerInPS(t *testing.T) {
	cases := map[string]struct {
		ps   string
		want bool
	}{
		"plain comm": {"login\nzsh\nmosh-server\nps\n", true},
		// macOS prints a full path for some processes.
		"full path": {"login\n/usr/local/Cellar/mosh/1.4.0_26/bin/mosh-server\n", true},
		// Some builds retitle the process.
		"retitled":       {"mosh-server: [mosh] server ...\n", true},
		"indented":       {"  mosh-server  \n", true},
		"no mosh":        {"login\nzsh\nscreen\nsshd\n", false},
		"empty":          {"", false},
		"mosh-client":    {"mosh-client\n", false},
		"lookalike name": {"my-mosh-server-wrapper\n", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, moshServerInPS(c.ps))
		})
	}
}

// TestIsMoshSDA guards the signature against the real replies of the terminals
// zlily is actually used through, all captured or documented rather than
// invented. Mosh's own value is the hardcoded one in CSI_SDA.
func TestIsMoshSDA(t *testing.T) {
	assert.True(t, isMoshSDA([]int{1, 10, 0}), "mosh's hardcoded reply")

	others := map[string][]int{
		"screen 4.00.03 (measured, mosh behind it)": {83, 40003, 0},
		"tmux":                    {84, 0, 0},
		"iTerm2":                  {0, 95, 0},
		"Terminal.app":            {1, 95, 0}, // same leading 1 as mosh
		"xterm":                   {41, 390, 0},
		"truncated/absent params": {},
	}
	for name, attrs := range others {
		t.Run(name, func(t *testing.T) {
			assert.False(t, isMoshSDA(attrs), "must not read as mosh")
		})
	}
}

func TestMoshHintLines(t *testing.T) {
	certain := moshHintLines(true, false)
	require.NotEmpty(t, certain)
	assert.Contains(t, strings.Join(certain, " "), "is running over mosh",
		"a self-identifying terminal is stated as fact")

	// The process check alone cannot tell whose session it is, so the wording
	// must stay hedged. Getting this backwards is the whole risk of the feature.
	maybe := moshHintLines(false, true)
	require.NotEmpty(t, maybe)
	assert.Contains(t, strings.Join(maybe, " "), "may be", "ps alone only supports a maybe")

	assert.Nil(t, moshHintLines(false, false), "nothing detected, nothing said")

	// Certainty wins over the weaker signal rather than doubling up.
	assert.Equal(t, certain, moshHintLines(true, true))

	for _, lines := range [][]string{certain, maybe} {
		assert.Contains(t, strings.Join(lines, " "), "%debug lastcol on",
			"the hint has to name the command it is suggesting")
	}
}

// TestMoshHintFlow drives the message sequence the login path produces.
func TestMoshHintFlow(t *testing.T) {
	newModel := func(t *testing.T) Model {
		t.Helper()
		logChan, _ := NewLogger()
		m := New(client.New(""), logChan)
		m.authMode = false
		return sizeTo(t, m, 80, 24)
	}
	feed := func(m Model, msgs ...tea.Msg) Model {
		for _, msg := range msgs {
			upd, _ := m.Update(msg)
			m = upd.(Model)
		}
		return m
	}
	lastOutput := func(m Model) string {
		if len(m.output) == 0 {
			return ""
		}
		if lines, ok := m.output[len(m.output)-1].Data.([]string); ok {
			return strings.Join(lines, " ")
		}
		return ""
	}

	t.Run("SDA identifies mosh", func(t *testing.T) {
		m := feed(newModel(t),
			uv.SecondaryDeviceAttributesEvent{1, 10, 0},
			moshPSMsg{found: true},
			moshDecideMsg{})
		assert.Contains(t, lastOutput(m), "is running over mosh")
	})

	t.Run("screen masks mosh, ps still hedges", func(t *testing.T) {
		m := feed(newModel(t),
			uv.SecondaryDeviceAttributesEvent{83, 40003, 0},
			moshPSMsg{found: true},
			moshDecideMsg{})
		assert.Contains(t, lastOutput(m), "may be")
	})

	t.Run("no mosh anywhere stays quiet", func(t *testing.T) {
		before := newModel(t)
		m := feed(before,
			uv.SecondaryDeviceAttributesEvent{0, 95, 0},
			moshPSMsg{found: false},
			moshDecideMsg{})
		assert.Len(t, m.output, len(before.output), "no hint appended")
	})

	t.Run("silent when the workaround is already on", func(t *testing.T) {
		before := newModel(t)
		before.reserveLastColumn = true
		m := feed(before,
			uv.SecondaryDeviceAttributesEvent{1, 10, 0},
			moshPSMsg{found: true},
			moshDecideMsg{})
		assert.Len(t, m.output, len(before.output),
			"nothing to suggest to someone who already turned it on")
	})

	t.Run("a late SDA reply is missed, ps carries it", func(t *testing.T) {
		// The grace period can expire first on a slow link; the hedged wording
		// is the correct fallback, not silence.
		m := feed(newModel(t), moshPSMsg{found: true}, moshDecideMsg{})
		assert.Contains(t, lastOutput(m), "may be")
	})
}
