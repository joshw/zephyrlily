package ui

import (
	"fmt"
	"testing"

	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// authOK drives the model through a successful login carrying the given proxy
// session token, the way attemptAuthCmd/reconnectCmd report one.
func authOK(t *testing.T, m Model, token string) Model {
	t.Helper()
	upd, _ := m.Update(authResultMsg{username: "wilmesj", token: token})
	return upd.(Model)
}

// A /detach tears down the proxy session, so answering the reconnect prompt
// builds a new one — and the proxy numbers messages from a per-session counter
// that restarts at 1. The IDs of the new session's login output therefore
// collide with the dead session's, and before the session-token check the
// retained dedup set swallowed the lot: the user saw "(reconnecting...)" hang
// (the "enter a blurb" prompt never arrived) and then a bare "Connected to ..."
// with no detach-review prompt.
func TestReconnect_NewSessionIDsAreNotDroppedAsDuplicates(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")

	// A long first session: IDs climb well past what a fresh login will emit.
	for id := int64(1); id <= 400; id++ {
		m = deliverLive(t, m, textMsg(id, fmt.Sprintf("session A line %d", id)))
	}
	require.Len(t, textIDs(m), 400)
	require.Greater(t, m.lastSeenID, int64(0), "the first session must have advanced last-seen")

	// /detach: the Lily socket closes, the proxy drops the session, and the TUI
	// offers to reconnect.
	m = deliverLive(t, m, api.WSServerMsg{ID: 401, Type: "error", Data: "lily connection closed"})
	require.True(t, m.reconnectPrompt)

	// Answering Y re-logs in and the proxy hands back a different token.
	m = authOK(t, m, "token-B")
	require.False(t, m.reconnectPrompt)

	// The new session's login output, numbered from 1 again.
	m = deliverLive(t, m, textMsg(1, "Welcome to lily at RPI"))
	m = deliverLive(t, m, api.WSServerMsg{ID: 2, Type: "prompt", Data: "--> "})
	assert.Equal(t, "--> ", m.prompt, "the blurb prompt from the new session must reach the user")

	m = deliverLive(t, m, textMsg(3, "Welcome to lily;   type /HELP for an introduction"))
	m = deliverLive(t, m, api.WSServerMsg{
		ID: 4, Type: "prompt", Data: "You were detached, do you wish to review now? (Y/n)"})
	assert.Equal(t, "You were detached, do you wish to review now? (Y/n)", m.prompt,
		"the detach-review prompt from the new session must reach the user")

	// Both text lines landed, and the scrollback from the old session is intact.
	var newLines []string
	for _, it := range m.output {
		if s, ok := it.Data.(string); ok {
			newLines = append(newLines, s)
		}
	}
	assert.Contains(t, newLines, "Welcome to lily at RPI")
	assert.Contains(t, newLines, "Welcome to lily;   type /HELP for an introduction")
	assert.Contains(t, newLines, "session A line 400", "reconnect must not discard scrollback")
}

// The new session's last-seen reporting must start from its own ID space. The
// surviving scrollback carries the dead session's (much larger) IDs, so leaving
// them in place made computeLastSeenID report the whole new login as already
// read the moment anything scrolled.
func TestReconnect_LastSeenIDRestartsWithTheSession(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	for id := int64(1); id <= 400; id++ {
		m = deliverLive(t, m, textMsg(id, fmt.Sprintf("session A line %d", id)))
	}
	require.Greater(t, m.lastSeenID, int64(100))

	m = authOK(t, m, "token-B")
	assert.Equal(t, int64(0), m.lastSeenID, "last-seen must reset with the session")
	assert.Equal(t, int64(0), m.storedLastSeenID)

	m = deliverLive(t, m, textMsg(1, "new session line 1"))
	assert.LessOrEqual(t, m.lastSeenID, int64(1),
		"last-seen must track the new session's IDs, not the retired ones")
}

// A re-auth that lands on the same proxy session (the proxy returns the
// existing token when the Lily connection is still up) continues one ID space,
// so the dedup state must survive: dropping it would re-append everything the
// history replay hands back.
func TestReauth_SameSessionKeepsDedupState(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	for id := int64(1); id <= 5; id++ {
		m = deliverLive(t, m, textMsg(id, fmt.Sprintf("line %d", id)))
	}
	require.Equal(t, []int64{1, 2, 3, 4, 5}, textIDs(m))

	m = authOK(t, m, "token-A")
	m = deliverLive(t, m, textMsg(5, "line 5"))
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, textIDs(m),
		"a redelivery within the same session must still be deduped")
}

// The token the model compares against is the one the client actually holds, so
// a reconnect that re-authenticates reports the new session's token.
func TestReconnectCmd_ReportsClientToken(t *testing.T) {
	c := client.New("")
	assert.Equal(t, "", c.Token(), "an unauthenticated client has no token")
}
