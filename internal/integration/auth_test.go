package integration

import (
	"testing"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/require"
)

// TestE2E_InfoReportsLilyAddress covers the one thing a client can ask before it
// has a token. The TUI needs it to find a saved password: passwords are keyed by
// Lily server, and in combined mode the proxy's own address is a fresh ephemeral
// port every run.
func TestE2E_InfoReportsLilyAddress(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	c := client.New(startProxy(t, fake))

	info, err := c.Info()
	require.NoError(t, err)
	require.Equal(t, fake.Addr(), info.LilyAddr)
	require.False(t, c.HasToken(), "/info must not need, or mint, a session")
}

// TestE2E_ExistingSessionRequiresPassword covers the shortcut in handleAuth that
// hands back the token of a session the user already has: it must check the
// supplied password first, or anyone who knows a username can take over that
// user's session on a proxy listening off loopback.
func TestE2E_ExistingSessionRequiresPassword(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	owner := client.New(proxyAddr)
	require.NoError(t, owner.Auth("alice", "password"))
	t.Cleanup(owner.Close)
	token := owner.Token()
	require.NotEmpty(t, token)

	// Wrong password for a live session: rejected, and told apart from other
	// failures so the TUI re-prompts instead of giving up.
	intruder := client.New(proxyAddr)
	err := intruder.Auth("alice", "not-the-password")
	require.ErrorIs(t, err, client.ErrAuthFailed)
	require.Empty(t, intruder.Token())

	// The rejection must not disturb the session it was checking — otherwise a
	// wrong password becomes a way to knock the user offline.
	st, err := owner.FetchState()
	require.NoError(t, err, "live session should survive a failed auth attempt")
	require.NotNil(t, st)

	// The real password still returns the same session, with no second login to
	// Lily (which would redirect the session to the new connection).
	again := client.New(proxyAddr)
	require.NoError(t, again.Auth("alice", "password"))
	require.Equal(t, token, again.Token())
}
