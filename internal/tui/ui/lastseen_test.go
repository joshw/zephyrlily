package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seenRecorder stands in for the proxy's /seen endpoint, keeping the last
// value a client reported.
type seenRecorder struct {
	last  atomic.Int64
	calls atomic.Int64
}

func (s *seenRecorder) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req api.SeenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.calls.Add(1)
		s.last.Store(req.LastSeenID)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// runCmds executes a command tree to completion, so that the HTTP calls a
// batched command makes actually happen.
func runCmds(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmds(t, c)
		}
	}
}

// A resume fetches the proxy's idea of last-seen so it can restore the scroll
// position to it. That number is only as fresh as the last report that reached
// the proxy, which during a long away spell is far behind what the client has
// actually displayed — and adopting it rewound lastSeenID, which was then
// reported straight back. The proxy ignores a lower value, so its mark stuck,
// and every later resume replayed the same span of history and printed the
// same "loaded N events from history" line. The user saw that line stack up.
func TestResume_DoesNotRewindLastSeenToTheProxysStaleMark(t *testing.T) {
	seen := &seenRecorder{}
	m := newDedupModel(t)
	m.client = client.New(seen.serve(t))
	m = authOK(t, m, "token-A")
	for id := int64(1); id <= 20; id++ {
		m = deliverLive(t, m, textMsg(id, "line"))
	}
	displayed := m.lastSeenID
	require.Greater(t, displayed, int64(10), "the live traffic must have advanced last-seen")

	m = dropSocket(t, m)
	m = authOK(t, m, "token-A") // same session: a silent resume
	upd, cmd := m.Update(initialStateMsg{state: &api.StateResponse{
		Whoami: "wilmesj", Server: "lily.example.org", LastSeenID: 5,
	}})
	m = upd.(Model)

	assert.Equal(t, displayed, m.lastSeenID,
		"a resume must not rewind last-seen to the proxy's stale copy")
	assert.Equal(t, int64(5), m.storedLastSeenID,
		"the stored mark still drives the scroll restore")

	runCmds(t, cmd)
	require.Positive(t, seen.calls.Load(), "the resume must report last-seen")
	assert.Equal(t, displayed, seen.last.Load(),
		"the proxy must be told what this client has seen, not handed its own stale mark back")
}

// The scroll restore still follows the stored mark forward: a client that
// reloaded into a fresh model has seen nothing, and the proxy's copy is then
// the only record of where the user had got to.
func TestResume_AdoptsAStoredMarkAheadOfThisModel(t *testing.T) {
	m := newDedupModel(t)
	m = authOK(t, m, "token-A")
	require.Zero(t, m.lastSeenID)

	upd, _ := m.Update(initialStateMsg{state: &api.StateResponse{
		Whoami: "wilmesj", Server: "lily.example.org", LastSeenID: 42,
	}})
	m = upd.(Model)

	assert.Equal(t, int64(42), m.lastSeenID, "a fresh model adopts the proxy's mark")
}
