package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A socket that goes away is all the TUI ever learns about a drop, and until
// the read error was kept there was nothing to put in a bug report: every
// recurring-disconnect snapshot said "proxy connection dropped" and no more.
// The close code is what separates a proxy that dropped us from a browser or a
// network that closed the connection itself.
func TestReadLoop_RecordsWhyTheSocketClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.Close(websocket.StatusGoingAway, "server going away")
	}))
	defer srv.Close()

	c := New(srv.Listener.Addr().String())
	require.NoError(t, c.Connect())
	assert.Empty(t, c.CloseReason(), "a live socket has no close reason")
	assert.False(t, c.ConnectedAt().IsZero(), "a connected client records when")

	select {
	case _, ok := <-c.Events:
		require.False(t, ok, "the channel closes when the read loop stops")
	case <-time.After(5 * time.Second):
		t.Fatal("the read loop did not notice the close")
	}

	assert.Contains(t, c.CloseReason(), "close code 1001",
		"the close code the peer sent must survive the read loop")
	assert.Contains(t, c.CloseReason(), "server going away")
}

// Each Client gets its own generation, which is how a message read off an
// abandoned client's channel is told apart from one off the current socket.
func TestClientGenerationsAreDistinct(t *testing.T) {
	a, b := New("localhost:1"), New("localhost:1")
	assert.NotEqual(t, a.Gen(), b.Gen())
	assert.True(t, a.ConnectedAt().IsZero(), "an unconnected client has no connect time")
}

func TestDescribeCloseErr(t *testing.T) {
	assert.Contains(t,
		describeCloseErr(websocket.CloseError{Code: websocket.StatusAbnormalClosure}),
		"close code 1006")
	assert.Equal(t, "read loop ended", describeCloseErr(nil))
	assert.Equal(t, "context canceled", describeCloseErr(context.Canceled))
}
