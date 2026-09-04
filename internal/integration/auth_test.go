package integration

import (
	"testing"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/require"
)

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
