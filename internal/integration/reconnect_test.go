package integration

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/require"
)

// A dropped socket is not a dropped session. The proxy session, its Lily
// connection and its event ring all outlive the WebSocket that was reading
// them, which is what lets the client put itself back together without asking
// the user anything — see Client.Resume and beginAutoReconnect.

func TestE2E_ResumeReattachesAfterTheSocketDies(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	c := client.New(proxyAddr)
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())
	token := c.Token()

	// The transport dies under a session that is still perfectly alive: a
	// laptop that slept, a tab the browser paused, an idle NAT entry reaped by
	// something in the middle. Nothing told the proxy the user is leaving.
	c.Close()

	nc, err := c.Resume()
	require.NoError(t, err, "the session is still there; re-attaching must work")
	t.Cleanup(nc.Close)

	require.Equal(t, token, nc.Token(),
		"a resume must re-attach to the session, not mint a new one — a new one "+
			"means a second Lily login and an ID space that restarts at 1")
	require.NoError(t, nc.Send("/who"), "the re-attached socket must be usable")

	select {
	case ev := <-nc.Events:
		require.NotNil(t, ev, "events channel closed instead of delivering")
	case <-time.After(5 * time.Second):
		t.Fatal("no event after re-attaching: the socket is not really connected")
	}
}

// What arrived while the socket was down is still recoverable afterwards: the
// proxy keeps buffering into the session's event ring with no subscriber
// attached, and the client's ordinary history replay reads it back.
func TestE2E_EventsDuringTheGapSurviveTheReconnect(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	c := client.New(proxyAddr)
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())

	st, err := c.FetchState()
	require.NoError(t, err)
	before := st.LastSeenID

	c.Close()

	// Said to a room nobody is currently listening in.
	fake.Push(lilytest.NotifyLine("public", lilytest.HandleAlice,
		[]string{lilytest.HandleCafe}, "GAPTOKEN said this while you were away"))

	nc, err := c.Resume()
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	require.Eventually(t, func() bool {
		events, _, err := nc.FetchEvents(before, 200)
		if err != nil {
			return false
		}
		for _, ev := range events {
			if containsToken(ev, "GAPTOKEN") {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond,
		"a message sent during the outage must still be readable after reconnecting")

	// And the socket is live again, not merely re-opened: what the client
	// backfilled would be worth little if the next thing said never arrived.
	fake.Push(lilytest.NotifyLine("public", lilytest.HandleAlice,
		[]string{lilytest.HandleCafe}, "LIVETOKEN and this one after"))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-nc.Events:
			require.True(t, ok, "the re-attached socket closed again")
			if containsToken(ev, "LIVETOKEN") {
				return
			}
		case <-deadline:
			t.Fatal("nothing arrived live after reconnecting: the subscription did not come back")
		}
	}
}

// The client leg carries Lily traffic and nothing else, so a quiet channel
// means a connection with no bytes on it — which is what Traefik, NAT tables
// and mobile carriers reap. The proxy pings to keep it warm; this checks that
// the pinging does not disturb the stream it is protecting.
func TestE2E_KeepalivePingsLeaveTheStreamAlone(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxyAddr := l.Addr().String()
	srv := api.New(api.Config{LilyAddr: fake.Addr(), WSPingInterval: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.RunWithListener(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-srvErr:
		case <-time.After(5 * time.Second):
			t.Error("proxy did not shut down in time")
		}
	})

	c := client.New(proxyAddr)
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)

	// Drain the login traffic, then sit idle across many ping cycles.
	drainEvents(t, c, 2*time.Second)
	time.Sleep(time.Second)

	// Still connected, and still delivering. A ping that never got its pong
	// would have blocked the write loop for wsPingTimeout and then torn the
	// connection down, so arriving promptly here is the assertion.
	fake.Push(lilytest.NotifyLine("public", lilytest.HandleAlice,
		[]string{lilytest.HandleCafe}, "PINGTOKEN still here"))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events:
			require.True(t, ok, "the keepalive closed a healthy connection")
			if containsToken(ev, "PINGTOKEN") {
				return
			}
		case <-deadline:
			t.Fatal("an idle connection stopped delivering: the keepalive broke the stream")
		}
	}
}

// A long paste is the other way the socket used to die without explanation.
// The library's default read limit is 32KiB and overrunning it closes the
// connection rather than rejecting the message, so one oversized line took the
// whole session down — silently, now that the client reconnects on its own.
func TestE2E_LargeCommandDoesNotKillTheSocket(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	c := client.New(startProxy(t, fake))
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)

	drainEvents(t, c, 2*time.Second)

	// Comfortably past the 32KiB default, and no larger than a real paste.
	require.NoError(t, c.Send(";cafe "+strings.Repeat("x", 64*1024)),
		"a long line must reach the proxy")

	// The socket has to still be there afterwards, which is the whole point:
	// before the limit was raised this line went nowhere because the
	// connection carrying it had already been closed underneath it.
	fake.Push(lilytest.NotifyLine("public", lilytest.HandleAlice,
		[]string{lilytest.HandleCafe}, "BIGTOKEN still connected"))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events:
			require.True(t, ok, "a long command closed the connection")
			if containsToken(ev, "BIGTOKEN") {
				return
			}
		case <-deadline:
			t.Fatal("nothing arrived after a long command: the socket died carrying it")
		}
	}
}

// containsToken reports whether a proxy event carries the given marker
// anywhere in it, without caring which of the message shapes it arrived as.
func containsToken(ev any, token string) bool {
	b, err := json.Marshal(ev)
	return err == nil && strings.Contains(string(b), token)
}

// drainEvents consumes whatever is queued until the stream goes quiet.
func drainEvents(t *testing.T, c *client.Client, quiet time.Duration) {
	t.Helper()
	for {
		select {
		case <-c.Events:
		case <-time.After(quiet):
			return
		}
	}
}
