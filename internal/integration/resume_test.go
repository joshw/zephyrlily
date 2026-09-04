package integration

import (
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/lilytest"
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
// cannot log back in — and must say so as an auth failure, because that is the
// answer the TUI turns into a login prompt. A generic error would instead offer
// a retry that could never succeed, which is how you get a client wedged on
// "reconnect failed" forever.
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
	require.ErrorIs(t, err, client.ErrAuthFailed,
		"a passwordless reconnect must ask for credentials, not offer a doomed retry")
	require.NotNil(t, nc, "a client must come back either way so the prompt has something to retry on")
	require.False(t, nc.HasToken())
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
