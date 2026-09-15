package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dropSocket delivers the nil event listenCmd yields when the proxy WebSocket
// has closed under the client.
func dropSocket(t *testing.T, m Model) Model {
	t.Helper()
	upd, cmd := m.Update(serverEventMsg{msg: nil})
	require.NotNil(t, cmd, "a dropped socket must schedule a reconnect")
	return upd.(Model)
}

// outputLines flattens the scrollback's plain string items.
func outputLines(m Model) []string {
	var lines []string
	for _, it := range m.output {
		if s, ok := it.Data.(string); ok {
			lines = append(lines, s)
		}
	}
	return lines
}

func containsLine(m Model, substr string) bool {
	for _, l := range outputLines(m) {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

// A socket that dies under a live proxy session is the transport's problem,
// not the user's: a slept laptop, a paused tab, an idle NAT entry. None of
// those are anything a "Reconnect? (Y/n)" can usefully be answered about, so
// the drop reconnects on its own and says nothing at all.
func TestAutoReconnect_DroppedSocketDoesNotAsk(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	before := len(m.output)

	m = dropSocket(t, m)

	assert.False(t, m.reconnectPrompt, "a transport drop must not raise the prompt")
	assert.True(t, m.authInProgress, "a reconnect must be under way")
	assert.Len(t, m.output, before, "a drop the client is fixing itself says nothing")
}

// The Lily session dying is a different thing: /detach is deliberate, and
// logging back in over it would fight the user. That one still asks.
func TestAutoReconnect_LilyCloseStillAsks(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")

	m = deliverLive(t, m, api.WSServerMsg{ID: 1, Type: "error", Data: "lily connection closed"})

	assert.True(t, m.reconnectPrompt, "a closed Lily session must still be the user's call")
}

// Re-attaching to the session it left reports nothing: same server, same
// identity, same ID space, and the events missed in the gap arrive through the
// ordinary history replay.
func TestAutoReconnect_SameSessionResumeIsSilent(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	m = dropSocket(t, m)
	before := len(m.output)

	m = authOK(t, m, "token-A") // Resume() handed back the same session
	require.True(t, m.quietResume, "a same-session resume must suppress the connection banner")

	upd, _ := m.Update(initialStateMsg{state: &api.StateResponse{
		Whoami: "wilmesj", Server: "lily.example.org",
	}})
	m = upd.(Model)

	assert.Equal(t, before, len(m.output), "a silent resume must not touch the scrollback")
	assert.False(t, containsLine(m, "Connected to"),
		"the connection banner reports nothing that changed")
	assert.False(t, m.quietResume, "the flag is consumed by the replay it applies to")
}

// A reconnect that had to build a new session does announce itself: new Lily
// login, new ID space, and output the user has not seen.
func TestAutoReconnect_NewSessionStillAnnounces(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	m = dropSocket(t, m)

	m = authOK(t, m, "token-B")
	require.False(t, m.quietResume)

	upd, _ := m.Update(initialStateMsg{state: &api.StateResponse{
		Whoami: "wilmesj", Server: "lily.example.org",
	}})
	m = upd.(Model)

	assert.True(t, containsLine(m, "Connected to"),
		"a fresh session is news and must be reported")
}

// A failure starts talking — the user needs to know why what they type is not
// going anywhere — but only once, however long the outage runs.
func TestAutoReconnect_RetriesQuietlyThenGivesUp(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	m = dropSocket(t, m)

	fail := func(m Model) Model {
		upd, cmd := m.Update(authResultMsg{err: errors.New("ws connect: dial tcp: refused")})
		require.NotNil(t, cmd, "a retry must still be scheduled")
		return upd.(Model)
	}

	m = fail(m)
	assert.False(t, m.reconnectPrompt, "the first failure retries rather than asking")
	assert.Equal(t, 1, m.reconnectAttempt)
	assert.True(t, containsLine(m, "connection lost"), "a real outage must be visible")

	lost := 0
	for i := 2; i <= autoReconnectAttempts; i++ {
		m = fail(m)
		require.Equalf(t, i, m.reconnectAttempt, "attempt %d", i)
		require.Falsef(t, m.reconnectPrompt, "attempt %d must not ask yet", i)
	}
	for _, l := range outputLines(m) {
		if strings.Contains(l, "connection lost") {
			lost++
		}
	}
	assert.Equal(t, 1, lost, "one outage is worth exactly one line")

	// Out of attempts: two minutes of failure is an outage, not a blip, and by
	// now the user is owed the truth rather than more silent waiting.
	upd, cmd := m.Update(authResultMsg{err: errors.New("ws connect: dial tcp: refused")})
	m = upd.(Model)
	assert.Nil(t, cmd, "giving up must stop retrying")
	assert.True(t, m.reconnectPrompt, "an outage that outlasts the retries hands back control")
	assert.True(t, containsLine(m, "reconnect failed"))
}

// Recovering after the outage was announced says so, since the announcement is
// still on screen above it.
func TestAutoReconnect_RecoveryAfterAnAnnouncedOutage(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	m = dropSocket(t, m)
	upd, _ := m.Update(authResultMsg{err: errors.New("dial tcp: refused")})
	m = upd.(Model)
	require.True(t, containsLine(m, "connection lost"))

	m = authOK(t, m, "token-A")

	assert.True(t, containsLine(m, "(reconnected)"))
	assert.Equal(t, 0, m.reconnectAttempt, "a success clears the retry state")
	assert.False(t, m.reconnectNotified)
	assert.True(t, m.quietResume, "the banner is still redundant on the same session")
}

// Credentials the proxy rejects can never be fixed by trying again, so that
// failure goes straight to the dialog instead of into the retry loop.
func TestAutoReconnect_RejectedCredentialsSkipTheRetries(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	m = dropSocket(t, m)

	upd, _ := m.Update(authResultMsg{err: fmt.Errorf("reconnect: %w", client.ErrAuthFailed)})
	m = upd.(Model)

	assert.True(t, m.authMode, "a rejected password needs the user, not another attempt")
	assert.False(t, m.reconnectPrompt)
	assert.Equal(t, 0, m.reconnectAttempt)
	assert.False(t, m.reconnectNotified)
}

func TestAutoReconnectDelay_BacksOffAndCaps(t *testing.T) {
	assert.Equal(t, 1*time.Second, autoReconnectDelay(1))
	assert.Equal(t, 2*time.Second, autoReconnectDelay(2))
	assert.Equal(t, 8*time.Second, autoReconnectDelay(4))
	assert.Equal(t, autoReconnectMaxDelay, autoReconnectDelay(autoReconnectAttempts))

	// The whole ladder has to be long enough to outlast a laptop finding its
	// Wi-Fi again, and short enough that a dead proxy is admitted to promptly.
	var total time.Duration
	for i := 1; i <= autoReconnectAttempts; i++ {
		total += autoReconnectDelay(i)
	}
	assert.Greater(t, total, 60*time.Second)
	assert.Less(t, total, 5*time.Minute)
}

// A session the proxy has disowned cannot be re-attached to, so that failure
// goes to the dialog as well — but it must not read as a rejected password,
// because nobody typed one.
func TestAutoReconnect_EndedSessionAsksForALogin(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	m = dropSocket(t, m)

	upd, _ := m.Update(authResultMsg{err: fmt.Errorf("resume: %w", client.ErrSessionGone)})
	m = upd.(Model)

	assert.True(t, m.authMode, "a session that has ended needs a fresh login")
	assert.False(t, m.reconnectPrompt)
	assert.NotContains(t, m.authError, "invalid username or password",
		"the user never gave a password here; do not tell them it was wrong")
}

// The failure this whole path exists for: a browser tab holding only a session
// token, whose socket dies for a second. Resume failing for a reason other than
// "the session is gone" must stay in the retry loop — logging in again would
// abandon a session that is still sitting on the proxy, and for a token-only
// client it cannot even do that, so the user lands in a login dialog because
// the network blinked.
func TestAutoReconnect_UnreachableProxyKeepsRetrying(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	m = dropSocket(t, m)

	upd, cmd := m.Update(authResultMsg{err: fmt.Errorf("resume: ws connect: %w", errors.New("dial tcp: refused"))})
	m = upd.(Model)

	assert.False(t, m.authMode, "a network blip must not demand credentials")
	assert.NotNil(t, cmd, "another attempt must be scheduled")
	assert.Equal(t, 1, m.reconnectAttempt)
	assert.True(t, containsLine(m, "connection lost"))
}
