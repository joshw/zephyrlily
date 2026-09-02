package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestMoshHintLines(t *testing.T) {
	// Nothing available to zlily can tell this session from another of the same
	// user's, so the wording stays hedged however sure we feel. Ghostty answering
	// with mosh's own VT220 claim is what settled that; see moshdetect.go.
	hint := moshHintLines(true)
	require.NotEmpty(t, hint)
	joined := strings.Join(hint, " ")
	assert.Contains(t, joined, "may be using mosh", "the claim stays hedged")
	assert.Contains(t, joined, "%debug lastcol on",
		"the hint has to name the command it is suggesting")

	// No process, no hint: this is the whole guard against telling a local
	// terminal user they are on mosh.
	assert.Nil(t, moshHintLines(false), "no mosh-server, nothing said")
}

// TestMoshHintFlow drives the message sequence the login path produces.
//
// The settle loop is stepped by feeding moshSettleMsg directly, so these run at
// full speed and assert the pacing rules rather than the wall clock.
func TestMoshHintFlow(t *testing.T) {
	newModel := func(t *testing.T) Model {
		t.Helper()
		logChan, _ := NewLogger()
		m := New(client.New(""), logChan)
		m.authMode = false
		m = sizeTo(t, m, 80, 24)
		m.moshProbed = true
		m.moshHintSettling = true
		m.moshHintOutputLen = len(m.output)
		return m
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

	t.Run("a mosh-server on the machine is worth a word", func(t *testing.T) {
		m := feed(newModel(t), moshPSMsg{found: true}, moshSettleMsg{})
		assert.Contains(t, lastOutput(m), "may be using mosh")
	})

	// The reported false positive: zlily run locally under a terminal that
	// answers device-attribute queries the way mosh does. Without a mosh-server
	// process there is nothing to say, whatever the terminal claims.
	t.Run("no mosh-server means silence", func(t *testing.T) {
		before := newModel(t)
		m := feed(before, moshPSMsg{found: false}, moshSettleMsg{})
		assert.Len(t, m.output, len(before.output), "no hint appended")
	})

	t.Run("silent when the workaround is already on", func(t *testing.T) {
		before := newModel(t)
		before.reserveLastColumn = true
		m := feed(before, moshPSMsg{found: true}, moshSettleMsg{})
		assert.Len(t, m.output, len(before.output),
			"nothing to suggest to someone who already turned it on")
	})

	// The reported bug: the hint landed between "Please enter a blurb" and the
	// answer. A pending prompt defers it, however long that takes.
	t.Run("waits out a pending prompt", func(t *testing.T) {
		before := newModel(t)
		before.moshPSFound = true
		before.prompt = "Please enter a blurb, or hit <enter> for none"

		m, cmd := before.moshHintSettle()
		assert.Len(t, m.output, len(before.output), "must not print over a prompt")
		assert.False(t, m.moshHintDone, "still waiting, not given up")
		require.NotNil(t, cmd, "must keep looking")

		// Prompt answered: the next tick is free to speak.
		m.prompt = ""
		m, _ = m.moshHintSettle()
		assert.Contains(t, lastOutput(m), "may be using mosh")
	})

	t.Run("gives up rather than outlast a prompt forever", func(t *testing.T) {
		before := newModel(t)
		before.moshPSFound = true
		before.prompt = "review now?"
		before.moshHintWaited = moshPromptCap

		m, cmd := before.moshHintSettle()
		assert.Len(t, m.output, len(before.output), "never printed")
		assert.True(t, m.moshHintDone, "gave up")
		assert.Nil(t, cmd, "and stopped ticking")
	})

	// Output still arriving means the review (or a conversation) is in flight.
	t.Run("waits for the output to go quiet", func(t *testing.T) {
		before := newModel(t)
		before.moshPSFound = true

		m := before
		for range 3 {
			m.output = append(m.output, OutputItem{Type: "text", Data: "review line"})
			var cmd tea.Cmd
			m, cmd = m.moshHintSettle()
			assert.NotEqual(t, "command", m.output[len(m.output)-1].Type,
				"deferred while output is still moving")
			require.NotNil(t, cmd, "and still looking")
		}

		// Nothing new this tick: now it speaks.
		m, _ = m.moshHintSettle()
		assert.Contains(t, lastOutput(m), "may be using mosh")
	})

	t.Run("a busy channel does not defer it forever", func(t *testing.T) {
		m := newModel(t)
		m.moshPSFound = true
		m.moshHintWaited = moshQuietCap
		m.output = append(m.output, OutputItem{Type: "text", Data: "still chatting"})

		m, _ = m.moshHintSettle()
		assert.Contains(t, lastOutput(m), "may be", "the cap wins over the lull")
	})

	t.Run("says it once", func(t *testing.T) {
		m := feed(newModel(t), moshPSMsg{found: true}, moshSettleMsg{})
		n := len(m.output)
		m = feed(m, moshSettleMsg{}, moshSettleMsg{})
		assert.Len(t, m.output, n, "later ticks add nothing")
	})
}
