package ui

import (
	"fmt"
	"testing"

	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDedupModel builds a sized model ready to receive proxy messages.
func newDedupModel(t *testing.T) Model {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m = sizeTo(t, m, 80, 24)
	return m
}

func textMsg(id int64, text string) api.WSServerMsg {
	return api.WSServerMsg{
		ID:   id,
		Type: "text",
		Data: map[string]interface{}{"text": text},
	}
}

func deliverLive(t *testing.T, m Model, msg api.WSServerMsg) Model {
	t.Helper()
	upd, _ := m.Update(serverEventMsg{msg: &msg})
	return upd.(Model)
}

// textIDs returns the IDs of all "text" output items that came from the proxy.
func textIDs(m Model) []int64 {
	var ids []int64
	for _, it := range m.output {
		if it.Type == "text" && it.ID != 0 {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

// A reattach streams login output (the detach review) live over the WebSocket
// while the state fetch blocks on the SLCP sync; the history replay then
// returns everything after the proxy's stored last-seen ID, overlapping what
// was already delivered. Each message must be incorporated exactly once.
func TestHistoryReplay_DoesNotDuplicateLiveEvents(t *testing.T) {
	m := newDedupModel(t)

	// Review lines 8..10 arrive live during login.
	for id := int64(8); id <= 10; id++ {
		m = deliverLive(t, m, textMsg(id, fmt.Sprintf("review line %d", id)))
	}

	// The state fetch completes: history replays everything after last-seen 6,
	// including id 7 (never delivered live) and 8..12 (8..10 already live).
	var events []api.WSServerMsg
	for id := int64(7); id <= 12; id++ {
		events = append(events, textMsg(id, fmt.Sprintf("review line %d", id)))
	}
	upd, _ := m.Update(initialStateMsg{
		state:  &api.StateResponse{Whoami: "#1603", Server: "RPI", EventBufSize: 12},
		events: events,
	})
	m = upd.(Model)

	// The live stream races the fetch and re-delivers the tail of the replay.
	m = deliverLive(t, m, textMsg(11, "review line 11"))
	m = deliverLive(t, m, textMsg(12, "review line 12"))
	// A genuinely new message afterward must still get through.
	m = deliverLive(t, m, textMsg(13, "fresh line 13"))

	ids := textIDs(m)
	seen := make(map[int64]int)
	for _, id := range ids {
		seen[id]++
	}
	for id := int64(7); id <= 13; id++ {
		assert.Equalf(t, 1, seen[id], "message id %d appended %d times", id, seen[id])
	}
}

// A prompt returned by the history replay was answered before the replay ran;
// re-applying it would resurrect a stale prompt in the input area.
func TestHistoryReplay_SkipsStalePrompts(t *testing.T) {
	m := newDedupModel(t)
	require.Equal(t, "", m.prompt)

	upd, _ := m.Update(initialStateMsg{
		state: &api.StateResponse{Whoami: "#1603", Server: "RPI", EventBufSize: 2},
		events: []api.WSServerMsg{
			{ID: 7, Type: "prompt", Data: "Please enter a blurb "},
			textMsg(8, "welcome"),
		},
	})
	m = upd.(Model)

	assert.Equal(t, "", m.prompt, "stale prompt from history replay must not be applied")
	assert.Equal(t, []int64{8}, textIDs(m), "non-prompt history must still be replayed")

	// A live prompt still works.
	upd, _ = m.Update(serverEventMsg{msg: &api.WSServerMsg{ID: 9, Type: "prompt", Data: "-> "}})
	m = upd.(Model)
	assert.Equal(t, "-> ", m.prompt)
}

// The dedup set must stay bounded over a long session: entries further behind
// the newest ID than the scrollback retains are pruned, while recent IDs keep
// suppressing duplicates.
func TestDedupSet_PrunesOldEntries(t *testing.T) {
	d := newDedupSet()
	total := int64(3 * dedupCap)
	for id := int64(1); id <= total; id++ {
		require.False(t, d.alreadyProcessed(&api.WSServerMsg{ID: id, Type: "text"}))
	}
	assert.LessOrEqual(t, len(d.seen), dedupCap+dedupCap/4,
		"dedup set must stay bounded")
	// Recent IDs are still suppressed; ancient ones (long out of scrollback)
	// have been forgotten and may be re-incorporated.
	assert.True(t, d.alreadyProcessed(&api.WSServerMsg{ID: total, Type: "text"}))
	assert.True(t, d.alreadyProcessed(&api.WSServerMsg{ID: total - int64(maxScrollback), Type: "text"}))
	assert.False(t, d.alreadyProcessed(&api.WSServerMsg{ID: 1, Type: "text"}))
}
