package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joshw/zephyrlily/internal/tui/client"
)

// noticeModel is a model past login, with nothing queued yet.
func noticeModel(t *testing.T) Model {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false
	return sizeTo(t, m, 80, 24)
}

// saying returns the text of the last output item, if it is a multi-line one.
func saying(m Model) string {
	if len(m.output) == 0 {
		return ""
	}
	if lines, ok := m.output[len(m.output)-1].Data.([]string); ok {
		return strings.Join(lines, " ")
	}
	return ""
}

// fixedNotice is a notice that always says the same thing.
func fixedNotice(text string) pendingNotice {
	return pendingNotice{name: text, itemType: "command", render: func(m Model) (Model, []string) {
		return m, []string{text}
	}}
}

func TestNoticeQueue(t *testing.T) {
	t.Run("one notice per quiet gap", func(t *testing.T) {
		m := noticeModel(t)
		m, cmd := m.queueNotice(fixedNotice("first"))
		require.NotNil(t, cmd, "queueing starts the loop")
		m, cmd = m.queueNotice(fixedNotice("second"))
		assert.Nil(t, cmd,
			"a second tick chain would advance the waited counter twice a tick")

		m, cmd = m.noticeSettle()
		assert.Equal(t, "first", saying(m))
		require.NotNil(t, cmd, "and keeps going for the one behind it")

		// The point of the gap: the notice just printed must not itself read as
		// movement, or the second would always wait an extra tick for nothing.
		m, cmd = m.noticeSettle()
		assert.Equal(t, "second", saying(m))
		assert.Nil(t, cmd, "queue empty, loop stopped")
	})

	t.Run("the loop stops when the queue empties", func(t *testing.T) {
		m := noticeModel(t)
		m, _ = m.queueNotice(fixedNotice("only"))
		m, _ = m.noticeSettle()
		assert.False(t, m.noticeSettling)

		// A stray tick after the fact is harmless.
		before := len(m.output)
		m, cmd := m.noticeSettle()
		assert.Len(t, m.output, before)
		assert.Nil(t, cmd)
	})

	t.Run("a notice can withdraw itself at fire time", func(t *testing.T) {
		// Whether a notice is still worth showing is a question about the state
		// it would be shown in, and minutes can pass before a quiet gap.
		m := noticeModel(t)
		withdrawn := pendingNotice{name: "withdrawn", render: func(m Model) (Model, []string) {
			return m, nil
		}}
		m, _ = m.queueNotice(withdrawn)
		m, _ = m.queueNotice(fixedNotice("shown"))

		before := len(m.output)
		m, cmd := m.noticeSettle()
		assert.Len(t, m.output, before, "withdrawn, so nothing printed")
		require.NotNil(t, cmd, "but the queue moves on")

		m, _ = m.noticeSettle()
		assert.Equal(t, "shown", saying(m))
	})

	t.Run("a notice can record that it fired", func(t *testing.T) {
		m := noticeModel(t)
		m, _ = m.queueNotice(pendingNotice{name: "marks", render: func(m Model) (Model, []string) {
			m.tipShown = true
			return m, []string{"marked"}
		}})
		m, _ = m.noticeSettle()
		assert.True(t, m.tipShown, "the render's Model is the one that continues")
	})

	// Each of these replaces the scrollback view or is about to move it, so a
	// notice appended into one lands somewhere the user is not looking.
	t.Run("waits for a view that shows the scrollback", func(t *testing.T) {
		for name, set := range map[string]func(Model) Model{
			"a pending prompt": func(m Model) Model { m.prompt = "review now?"; return m },
			"the login dialog": func(m Model) Model { m.authMode = true; return m },
			"the editor":       func(m Model) Model { m.editMode = true; return m },
			"history search":   func(m Model) Model { m.searchMode = true; return m },
			"reconnect prompt": func(m Model) Model { m.reconnectPrompt = true; return m },
		} {
			t.Run(name, func(t *testing.T) {
				m := noticeModel(t)
				m, _ = m.queueNotice(fixedNotice("held"))
				m = set(m)

				before := len(m.output)
				m, cmd := m.noticeSettle()
				assert.Len(t, m.output, before, "must not print into "+name)
				require.NotNil(t, cmd, "but keeps looking")
				assert.Len(t, m.pendingNotices, 1, "still queued, not dropped")
			})
		}
	})

	// Being scrolled back is deliberately not one of them: a notice is appended
	// at the end of the scrollback, so it is waiting where the reader is headed
	// rather than lost behind them.
	t.Run("scrolled back is not a reason to wait", func(t *testing.T) {
		m := noticeModel(t)
		for range 200 {
			m.output = append(m.output, OutputItem{Type: "text", Data: "line"})
		}
		m = m.syncViewportContent()
		m.viewport.GotoTop()
		require.False(t, m.viewport.AtBottom(), "the premise of the test")

		m, _ = m.queueNotice(fixedNotice("said anyway"))
		m.noticeOutputLen = len(m.output) // nothing moved since queueing
		m, _ = m.noticeSettle()
		assert.Equal(t, "said anyway", saying(m))
	})

	t.Run("gives up rather than hold a timer all session", func(t *testing.T) {
		m := noticeModel(t)
		m, _ = m.queueNotice(fixedNotice("abandoned"))
		m.prompt = "an unanswered question"
		m.noticeWaited = noticeBusyCap

		before := len(m.output)
		m, cmd := m.noticeSettle()
		assert.Len(t, m.output, before, "never printed")
		assert.Empty(t, m.pendingNotices, "dropped")
		assert.Nil(t, cmd, "and stopped ticking")
	})

	t.Run("giving up on one still shows the next", func(t *testing.T) {
		m := noticeModel(t)
		m, _ = m.queueNotice(fixedNotice("abandoned"))
		m, _ = m.queueNotice(fixedNotice("survivor"))
		m.prompt = "an unanswered question"
		m.noticeWaited = noticeBusyCap

		m, cmd := m.noticeSettle()
		require.NotNil(t, cmd)
		require.Len(t, m.pendingNotices, 1)

		m.prompt = ""
		m.noticeWaited = 0
		m.noticeOutputLen = len(m.output)
		m, _ = m.noticeSettle()
		assert.Equal(t, "survivor", saying(m))
	})
}

func TestQueueSessionNotices(t *testing.T) {
	t.Run("waits for both the state fetch and the stored setting", func(t *testing.T) {
		m := noticeModel(t)

		m.stateReady = true
		m, cmd := m.queueSessionNoticesWhenReady()
		assert.Nil(t, cmd, "the setting has not been read yet")
		assert.Empty(t, m.pendingNotices)

		m.tipsLoaded = true
		m, cmd = m.queueSessionNoticesWhenReady()
		require.NotNil(t, cmd)
		assert.NotEmpty(t, m.pendingNotices)
	})

	t.Run("either order", func(t *testing.T) {
		m := noticeModel(t)
		m.tipsLoaded = true
		m, cmd := m.queueSessionNoticesWhenReady()
		assert.Nil(t, cmd, "the state fetch has not returned yet")

		m.stateReady = true
		m, _ = m.queueSessionNoticesWhenReady()
		assert.NotEmpty(t, m.pendingNotices)
	})

	// A reconnect is a new proxy session but the same person at the terminal,
	// and it runs the same post-login path.
	t.Run("queues once per session, not per reconnect", func(t *testing.T) {
		m := noticeModel(t)
		m.stateReady, m.tipsLoaded = true, true
		m, _ = m.queueSessionNoticesWhenReady()
		n := len(m.pendingNotices)

		m, cmd := m.queueSessionNoticesWhenReady()
		assert.Len(t, m.pendingNotices, n, "nothing queued again")
		assert.Nil(t, cmd)
	})

	// The hint is conditional and actionable; a tip is neither, so it waits.
	t.Run("the mosh hint comes before the tip", func(t *testing.T) {
		m := noticeModel(t)
		m.moshProbed, m.moshPSFound = true, true
		m.stateReady, m.tipsLoaded = true, true
		m, _ = m.queueSessionNoticesWhenReady()
		require.Len(t, m.pendingNotices, 2)

		m, _ = m.noticeSettle()
		assert.Contains(t, saying(m), "may be using mosh")

		m, _ = m.noticeSettle()
		assert.Contains(t, saying(m), "Tip: ")
	})

	t.Run("no mosh probe means only the tip", func(t *testing.T) {
		m := noticeModel(t)
		m.stateReady, m.tipsLoaded = true, true
		m, _ = m.queueSessionNoticesWhenReady()
		require.Len(t, m.pendingNotices, 1)

		m, _ = m.noticeSettle()
		assert.Contains(t, saying(m), "Tip: ")
		assert.True(t, m.tipShown)
	})

	t.Run("%tips off arriving late still silences it", func(t *testing.T) {
		// A '%tips off' in a zlilyStartup memo reaches the client as a
		// clientcommand at an unknowable moment after login, which is why the
		// check is at fire time rather than at queueing time.
		m := noticeModel(t)
		m.stateReady, m.tipsLoaded = true, true
		m, _ = m.queueSessionNoticesWhenReady()
		require.NotEmpty(t, m.pendingNotices)

		m, _, _, ok := m.applyLocalCommand("%tips off")
		require.True(t, ok)
		require.False(t, m.tipsEnabled)

		before := len(m.output)
		m, _ = m.noticeSettle()
		assert.Len(t, m.output, before, "the tip withdrew")
		assert.False(t, m.tipShown)
	})
}

// The login path has to produce the messages the queueing waits on, or a real
// session gets no tip however well the queue works.
func TestSessionTipReachesTheQueue(t *testing.T) {
	m := noticeModel(t)
	m.stateReady = true

	upd, _ := m.Update(tipsLoadedMsg{off: false})
	m = upd.(Model)
	assert.True(t, m.tipsLoaded)
	assert.True(t, m.tipsEnabled)
	assert.NotEmpty(t, m.pendingNotices, "the tip is queued once both arrive")

	m, _ = m.noticeSettle()
	assert.Contains(t, saying(m), "Tip: ")
}

func TestTipsLoadedMsgHonoursOff(t *testing.T) {
	m := noticeModel(t)
	m.stateReady = true

	upd, _ := m.Update(tipsLoadedMsg{off: true})
	m = upd.(Model)
	require.False(t, m.tipsEnabled)

	before := len(m.output)
	m, _ = m.noticeSettle()
	assert.Len(t, m.output, before, "a stored 'off' means no tip at login")
}

// A tip is ruled off because it arrived unasked; the mosh hint is advice about
// the display and reads as ordinary command output.
func TestNoticeItemTypes(t *testing.T) {
	assert.Equal(t, "tip", sessionTipNotice().itemType)
	assert.Equal(t, "command", moshHintNotice().itemType)

	m := noticeModel(t)
	m.stateReady, m.tipsLoaded = true, true
	m, _ = m.queueSessionNoticesWhenReady()
	m, _ = m.noticeSettle()
	require.NotEmpty(t, m.output)
	assert.Equal(t, "tip", m.output[len(m.output)-1].Type,
		"the queued tip is appended as a boxed item")
}
