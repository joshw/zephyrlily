package integration

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/require"
)

// The browser client keeps its session token across page loads, because the
// proxy session outlives the page: a reload, or a tab discarded overnight, ends
// the program while the session is still live. These cover the resume path it
// takes on the next load.

func TestE2E_ResumeSessionReattaches(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	first := client.New(proxyAddr)
	require.NoError(t, first.Auth("alice", "password"))
	t.Cleanup(first.Close)
	token := first.Token()

	// A fresh client — the page was reloaded — with nothing but the token.
	resumed := client.New(proxyAddr)
	user, err := resumed.ResumeSession(token)
	require.NoError(t, err)
	require.Equal(t, "alice", user, "resume should report whose session it is")
	require.True(t, resumed.HasToken())
	require.Equal(t, token, resumed.Token(), "resuming must not mint a new session")

	// And it is a working session, not merely a valid-looking token: no second
	// Lily login happened, so state is there to be read.
	st, err := resumed.FetchState()
	require.NoError(t, err)
	require.NotNil(t, st)

	// Crucially, the WebSocket must be open too. Resuming used to restore only
	// the token, which left a client that looked authenticated, received no
	// events, and panicked on the first command typed — a nil socket
	// dereference from inside Update, which takes the whole TUI down.
	require.NoError(t, resumed.Send("/who"),
		"a resumed session must have its WebSocket open, not just its token")

	select {
	case ev := <-resumed.Events:
		require.NotNil(t, ev, "events channel closed instead of delivering")
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived on a resumed session: the socket is not really connected")
	}
}

func TestE2E_ResumeRejectsUnknownToken(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	c := client.New(startProxy(t, fake))

	// ErrAuthFailed specifically: the TUI keys "show the login dialog" off it,
	// and the browser page keys "discard the stored token" off the same answer.
	_, err := c.ResumeSession("0123456789abcdef0123456789abcdef")
	require.ErrorIs(t, err, client.ErrAuthFailed)
	require.False(t, c.HasToken(), "a rejected token must not be retained")
	require.Empty(t, c.Token())
}

func TestE2E_ResumeRejectsEmptyToken(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	c := client.New(startProxy(t, fake))

	_, err := c.ResumeSession("")
	require.Error(t, err)
	require.False(t, c.HasToken())
}

// A page that loads while the network is still coming up must not be treated
// as a rejected token: the browser answers ErrAuthFailed by deleting the token
// it has stored, and deleting it here strands a session that is still on the
// proxy, with nothing left to resume it by on the next load.
func TestE2E_ResumeSessionDistinguishesAnUnreachableProxy(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	addr, stopProxy := startStoppableProxy(t, fake)

	first := client.New(addr)
	require.NoError(t, first.Auth("alice", "password"))
	token := first.Token()
	stopProxy()

	_, err := client.New(addr).ResumeSession(token)
	require.Error(t, err)
	require.NotErrorIs(t, err, client.ErrAuthFailed,
		"an unreachable proxy is not a rejected token; the stored token must survive it")
}

// A token from one proxy must not open a session on another, even though both
// front the same Lily server — tokens name a session, not a user.
func TestE2E_ResumeRejectsTokenFromAnotherProxy(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())

	a := client.New(startProxy(t, fake))
	require.NoError(t, a.Auth("alice", "password"))
	t.Cleanup(a.Close)

	other := client.New(startProxy(t, fake))
	_, err := other.ResumeSession(a.Token())
	require.ErrorIs(t, err, client.ErrAuthFailed)
}

// A resumed client has a token but no password. If its session later ends, it
// cannot log back in — and must say so as ErrSessionGone, because that is one
// of the two answers the TUI turns into a login prompt. A generic error would
// instead offer a retry that could never succeed, which is how you get a client
// wedged on "reconnect failed" forever; ErrAuthFailed would be a lie, telling a
// user who typed no password that the password was wrong.
func TestE2E_ResumedClientCannotReconnectSilently(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	first := client.New(proxyAddr)
	require.NoError(t, first.Auth("alice", "password"))
	t.Cleanup(first.Close)

	resumed := client.New(proxyAddr)
	_, err := resumed.ResumeSession(first.Token())
	require.NoError(t, err)

	nc, err := resumed.Reconnect()
	require.ErrorIs(t, err, client.ErrSessionGone,
		"a passwordless reconnect must ask for credentials, not offer a doomed retry")
	require.NotErrorIs(t, err, client.ErrAuthFailed,
		"nothing was rejected here, and saying so accuses the user of a bad password")
	require.NotNil(t, nc, "a client must come back either way so the prompt has something to retry on")
	require.False(t, nc.HasToken())
}

// Resume has to tell "the proxy has forgotten this session" apart from "the
// network was away for a second", because the caller answers the first by
// discarding the session and asking for a password. The WebSocket dial cannot
// tell them apart — in the browser build it never can, since the JS WebSocket
// API hides the handshake status — so the distinction has to come from
// somewhere the status is visible.
func TestE2E_ResumeReportsAGenuinelyDeadSession(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	c := client.New(proxyAddr)
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())

	// Killing Lily takes the proxy session with it; the token now names nothing.
	fake.Close()
	require.Eventually(t, func() bool {
		_, err := client.New(proxyAddr).ResumeSession(c.Token())
		return err != nil
	}, 5*time.Second, 50*time.Millisecond, "the proxy should drop the session when Lily goes")

	nc, err := c.Resume()
	t.Cleanup(nc.Close)
	require.ErrorIs(t, err, client.ErrSessionGone)
}

// The other half: a proxy that cannot be reached at all must NOT be read as a
// dead session. Answering a blip that way costs the user a session that is
// still sitting on the proxy — the stored token is discarded and a browser tab,
// which has nothing but that token, is dumped at the login dialog.
func TestE2E_ResumeDoesNotMistakeAnOutageForADeadSession(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())

	addr, stopProxy := startStoppableProxy(t, fake)

	c := client.New(addr)
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())

	// The proxy goes away while the session it was serving does not: a restart,
	// a load balancer moving, a laptop whose Wi-Fi dropped. Lily is untouched.
	stopProxy()

	nc, err := c.Resume()
	t.Cleanup(nc.Close)
	require.Error(t, err)
	require.NotErrorIs(t, err, client.ErrSessionGone,
		"an unreachable proxy proves nothing about the session; keep retrying on the token")
	require.True(t, nc.HasToken(), "the token must survive so the retry has something to resume")
}

// startStoppableProxy is startProxy with the shutdown exposed, for tests that
// need the proxy to vanish mid-session.
func startStoppableProxy(t *testing.T, fake *lilytest.Server) (string, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()

	srv := api.New(api.Config{LilyAddr: fake.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.RunWithListener(ctx, l) }()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-errCh:
			case <-time.After(5 * time.Second):
				t.Error("proxy did not shut down in time")
			}
		})
	}
	t.Cleanup(stop)
	return addr, stop
}

// Send on a client that has a token but never connected must return an error
// rather than dereferencing a nil socket. Auth and Connect are separate steps,
// so this state is reachable, and a panic here happens inside the TUI's Update
// and destroys the session.
func TestE2E_SendWithoutConnectIsAnError(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	c := client.New(startProxy(t, fake))
	require.NoError(t, c.Auth("alice", "password"))
	t.Cleanup(c.Close)
	// Deliberately no Connect().
	require.Error(t, c.Send("/who"))
}

// Shortening, expanding and previewing all happen on the proxy, whichever
// client asked. The terminal client used to do it itself, which meant a build
// from source had no credential and was refused — and put the credential in
// every client binary rather than in the one place that needs it.
func TestE2E_TerminalClientShortensThroughTheProxy(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	c := client.New(startProxy(t, fake))
	require.NoError(t, c.Auth("alice", "password"))
	t.Cleanup(c.Close)

	// The proxy answers these routes; reaching them at all is the point, and a
	// network failure past that is the upstream service's business.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := c.Shorten(ctx, "", "https://example.com/something")
	require.NotContains(t, fmt.Sprint(err), "does not support",
		"the proxy should expose /shorten")

	_, err = c.ExpandShortURL(ctx, "https://da.gd/aaaa")
	require.NotContains(t, fmt.Sprint(err), "does not support",
		"the proxy should expose /urlexpand")

	_, err = c.Preview(ctx, "https://example.com/")
	require.NotContains(t, fmt.Sprint(err), "does not support",
		"the proxy should expose /urlpreview")
}
