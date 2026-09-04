package integration

import (
	"testing"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/require"
)

// browserFixture stands up a fake Lily and a proxy, and returns the proxy
// address plus a token for a live session on it.
func browserFixture(t *testing.T) (proxyAddr, token string) {
	t.Helper()
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr = startProxy(t, fake)

	owner := client.New(proxyAddr)
	require.NoError(t, owner.Auth("alice", "password"))
	t.Cleanup(owner.Close)
	return proxyAddr, owner.Token()
}

// The page starts with nothing stored: the credential dialog, as always.
func TestBrowser_NoStoredTokenShowsLogin(t *testing.T) {
	proxyAddr, _ := browserFixture(t)

	res := runBrowser(t, proxyAddr, "", browserStep{Wait: 2500})

	require.True(t, res.loginDialogShowing(), "expected the login dialog\n%s", res)
	require.Zero(t, res.Cleared, "nothing was stored, so nothing should have been cleared")
	// No "Remember password" box: the browser build has nowhere to put a
	// password, so the dialog omits it (see credsStorable).
	require.False(t, res.contains("Remember password"),
		"the browser build cannot store a password and must not offer to\n%s", res)
	res.requireAligned(t, "Username:", "Password:", "Tab: switch", "Enter: log in")
}

// A token from a live session skips the login entirely. This is the whole point
// of storing one: logging in again would only have handed back the same
// session, since handleAuth returns the existing token when the password checks
// out.
func TestBrowser_ResumesStoredSession(t *testing.T) {
	proxyAddr, token := browserFixture(t)

	res := runBrowser(t, proxyAddr, token, browserStep{Wait: 3000})

	require.False(t, res.loginDialogShowing(), "a valid token must not produce a login prompt\n%s", res)
	require.Zero(t, res.Cleared, "a working token must not be discarded")
	require.True(t, res.contains("Resumed your session"), "expected the resume notice\n%s", res)
	// The status bar only renders once the session is actually up.
	require.Contains(t, res.row(browserRows-2), "TestServer", "expected the status bar\n%s", res)
	require.False(t, res.contains("not connected"),
		"resumed but with no usable connection\n%s", res)
}

// The regression that shipped: resuming restored the token but not the
// WebSocket, so the client looked authenticated, received nothing, and panicked
// on the first Enter. Sending a command is the only assertion that catches it —
// anything using HTTP alone passes against a client that cannot send at all.
func TestBrowser_ResumedSessionCanSendCommands(t *testing.T) {
	proxyAddr, token := browserFixture(t)

	res := runBrowser(t, proxyAddr, token,
		browserStep{Wait: 3000},
		browserStep{Write: "/who"},
		browserStep{Write: "\r"},
		browserStep{Wait: 2000},
	)

	require.True(t, res.contains("/who"), "the typed command should be echoed\n%s", res)

	// Assert on output only Lily could have produced. Not the username: the
	// resume notice says "Resumed your session as alice", so matching that
	// passes against a client whose socket was never opened — which is exactly
	// how this regression got through the first time.
	require.True(t, res.contains("Users here:"),
		"expected /who output from Lily; the command never reached it\n%s", res)
	require.False(t, res.contains("not connected"),
		"the client reported it had no connection\n%s", res)

	// Appended output is where the newline translation shows: see
	// requireLeftAligned.
	res.requireLeftAligned(t, "Users here:", "  alice", "  bob", "  carol")
}

// A token the proxy has never heard of must be dropped, not retried on every
// load, and must fall back to the dialog rather than to a broken session.
func TestBrowser_StaleTokenFallsBackToLogin(t *testing.T) {
	proxyAddr, _ := browserFixture(t)

	res := runBrowser(t, proxyAddr,
		"0000000000000000000000000000000000000000000000000000000000000000",
		browserStep{Wait: 3000})

	require.True(t, res.loginDialogShowing(), "expected the login dialog\n%s", res)
	require.Equal(t, 1, res.Cleared, "a rejected token must be discarded exactly once")
}

// Logging in through the dialog: proves the keyboard path works end to end —
// xterm.js-style byte input reaching Bubble Tea's parser, field cycling, and
// submission — and that the resulting token is offered to the host for storage.
func TestBrowser_LoginThroughDialogStoresToken(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	res := runBrowser(t, proxyAddr, "",
		browserStep{Wait: 2500},
		browserStep{Write: "alice"},
		browserStep{Write: "\t"}, // to the password field
		browserStep{Write: "password"},
		browserStep{Write: "\r"},
		browserStep{Wait: 3000},
	)

	require.False(t, res.loginDialogShowing(), "the dialog should be gone after logging in\n%s", res)
	require.Contains(t, res.row(browserRows-2), "TestServer", "expected the status bar\n%s", res)
	require.NotEmpty(t, res.Saved, "a successful login must offer its token to the host")
	require.Len(t, res.Saved[0], 64, "a proxy session token is 32 hex-encoded bytes")
}

// Geometry changes arrive through a JS call rather than SIGWINCH, so the
// reflow path is browser-specific and worth asserting on directly.
func TestBrowser_ResizeReflows(t *testing.T) {
	proxyAddr, token := browserFixture(t)

	res := runBrowser(t, proxyAddr, token,
		browserStep{Wait: 3000},
		browserStep{Resize: []int{70, 20}},
		browserStep{Wait: 1200},
	)

	// The status bar is laid out to the reported width; at 70 columns it must
	// not still be drawn for 100.
	require.NotEmpty(t, res.screen)
	for _, line := range res.screen {
		require.LessOrEqual(t, len([]rune(line)), 70,
			"content wider than the terminal after a resize — the reflow did not happen\n%s", res)
	}
}

// The logo is 256-colour art that paints its own black background and black
// glyphs, which on a terminal that is not black leaves visible dark patches.
// The client asks the terminal what colour it is and lifts the art's black
// point onto it — but only if the reply gets back, which depends on the page
// piping the terminal's responses into the client. It is the same path
// keystrokes take, so a regression here would be silent.
func TestBrowser_SplashIsRecolouredToTheTerminalBackground(t *testing.T) {
	proxyAddr, _ := browserFixture(t)

	res := runBrowserWithBackground(t, proxyAddr, "", "#16161a", browserStep{Wait: 3000})
	require.True(t, res.AnsweredBackground,
		"the client never asked what colour the terminal is")

	// Truecolour in the stream is the signature of the lift: the art itself is
	// 256-colour, so nothing else would emit it.
	require.Contains(t, string(res.raw), "\x1b[38;2;",
		"the splash was not recoloured against the terminal background")
}

// A terminal that does not answer must still get a sensible splash, not a hang
// or a half-applied transformation.
func TestBrowser_SplashSurvivesAnUnansweredQuery(t *testing.T) {
	proxyAddr, _ := browserFixture(t)

	res := runBrowser(t, proxyAddr, "", browserStep{Wait: 3000})
	require.False(t, res.AnsweredBackground)
	require.True(t, res.loginDialogShowing(), "the client should carry on regardless\n%s", res)
}
