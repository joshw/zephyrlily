package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsSeenRecorder is a proxy that accepts a WebSocket and reports the seen
// values pushed down it.
func wsSeenRecorder(t *testing.T) (addr string, seen <-chan int64) {
	t.Helper()
	ch := make(chan int64, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		for {
			var cm api.WSClientMsg
			if err := wsjson.Read(r.Context(), ws, &cm); err != nil {
				return
			}
			if cm.Type == "seen" {
				ch <- cm.LastSeenID
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String(), ch
}

// The mark now goes out the moment it moves, over the socket the model already
// has. Nothing here waits on a timer, which is the point: the five-second
// ReportSeen loop this replaced could go an hour without firing in a browser
// tab the user had left in the background, and the proxy's mark went stale
// enough that every reconnect replayed the same stretch of history.
func TestSeenMark_IsPushedOverTheSocketAsItMoves(t *testing.T) {
	addr, seen := wsSeenRecorder(t)
	c := client.New(addr)
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)

	m := newDedupModel(t)
	m.client = c
	m = authOK(t, m, "token-A")
	for id := int64(1); id <= 5; id++ {
		m = deliverLive(t, m, textMsg(id, "line"))
	}
	require.Positive(t, m.lastSeenID, "the events must have advanced the mark")

	select {
	case got := <-seen:
		assert.Positive(t, got, "the pushed mark must name an event the model displayed")
	case <-time.After(5 * time.Second):
		t.Fatal("displaying events reported nothing to the proxy")
	}
}
