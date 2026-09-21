package integration

import (
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lastSeenAt polls the proxy for the mark it holds for this client's session.
func lastSeenAt(t *testing.T, c *client.Client) int64 {
	t.Helper()
	st, err := c.FetchState()
	require.NoError(t, err)
	return st.LastSeenID
}

// The seen report used to be an HTTP POST, which cost enough that the TUI only
// sent one every five seconds — and a browser tab in the background does not
// run timers, so the mark went stale and stayed stale for hours. It now rides
// the WebSocket that is already open, which is cheap enough to send the moment
// the mark moves.
func TestE2E_SeenReportRidesTheWebSocket(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	c := client.New(proxyAddr)
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)

	require.NoError(t, c.PushSeen(7))
	require.Eventually(t, func() bool { return lastSeenAt(t, c) == 7 }, 5*time.Second, 20*time.Millisecond,
		"a seen report sent over the socket must reach the proxy")

	// A report behind the mark is ignored rather than rewinding it: a client
	// catching up from a resume replays what the proxy already knows it saw.
	require.NoError(t, c.PushSeen(3))
	require.NoError(t, c.PushSeen(9))
	require.Eventually(t, func() bool { return lastSeenAt(t, c) == 9 }, 5*time.Second, 20*time.Millisecond)
	assert.EqualValues(t, 9, lastSeenAt(t, c), "the lower report must not have moved the mark")

	// The endpoint stays, for a client with no socket to push down — and for
	// one built before the socket carried these at all.
	require.NoError(t, c.ReportSeen(11))
	assert.EqualValues(t, 11, lastSeenAt(t, c), "POST /seen must still work")
}

// A client with no socket says so rather than pretending the report landed:
// the caller's fallback is the HTTP endpoint.
func TestE2E_PushSeenNeedsASocket(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	c := client.New(proxyAddr)
	require.Error(t, c.PushSeen(4), "an unconnected client cannot push a seen report")
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)
	require.NoError(t, c.PushSeen(4))
}
