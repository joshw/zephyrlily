package integration

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/tui/ui"
	"github.com/stretchr/testify/require"
)

// startStack boots a fake Lily server, a real proxy on an ephemeral loopback
// port pointing at it, and an authenticated, connected client. Everything is
// torn down via t.Cleanup (LIFO: client first, then proxy, then fake).
func startStack(t *testing.T) (*client.Client, *lilytest.Server) {
	return startStackWith(t, lilytest.DefaultWorld())
}

// startStackWith is startStack with a caller-supplied fake-Lily world.
func startStackWith(t *testing.T, opt lilytest.Options) (*client.Client, *lilytest.Server) {
	t.Helper()
	fake := lilytest.Start(t, opt)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxyAddr := l.Addr().String()

	srv := api.New(api.Config{LilyAddr: fake.Addr()})
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
	return c, fake
}

// startUI builds the real tui model wired to c and runs it under teatest.
func startUI(t *testing.T, c *client.Client) *teatest.TestModel {
	t.Helper()
	logChan, _ := ui.NewLogger()
	m := ui.New(c, logChan)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))
	t.Cleanup(func() {
		_ = tm.Quit()
		tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
	})
	return tm
}

func waitForOutput(t *testing.T, tm *teatest.TestModel, subs ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, s := range subs {
			if !bytes.Contains(b, []byte(s)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(25*time.Millisecond))
}

func TestE2E_InitialStateRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping teatest end-to-end test in -short mode")
	}
	c, _ := startStack(t)
	tm := startUI(t, c)

	// The connected banner comes from GET /state, which blocks until the fake
	// completes its sync — exercising auth + handshake + state fetch.
	waitForOutput(t, tm, "Connected to TestServer", "me")
}

func TestE2E_InboundPublicPrivateEmote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping teatest end-to-end test in -short mode")
	}
	c, fake := startStack(t)
	tm := startUI(t, c)
	waitForOutput(t, tm, "Connected to TestServer")

	fake.Push(
		lilytest.NotifyLine("public", lilytest.HandleAlice, []string{lilytest.HandleCafe}, "PUBTOKEN hi"),
		lilytest.NotifyLine("private", lilytest.HandleBob, []string{lilytest.HandleMe}, "PRIVTOKEN hey"),
		lilytest.NotifyLine("emote", lilytest.HandleCarol, []string{lilytest.HandleCafe}, " EMOTETOKEN waves"),
	)

	// Each event renders through the full path (lily -> proxy fan-out -> ws ->
	// client -> formatEvent) with names resolved from the synced entities.
	waitForOutput(t, tm,
		"PUBTOKEN", "alice", "cafe",
		"PRIVTOKEN", "bob",
		"EMOTETOKEN", "carol",
	)
}

func TestE2E_OutboundCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping teatest end-to-end test in -short mode")
	}
	c, _ := startStack(t)
	tm := startUI(t, c)
	waitForOutput(t, tm, "Connected to TestServer")

	tm.Type("/who")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	// The fake replies to /who; the proxy buffers %begin..%end into a
	// commandresult the UI renders.
	waitForOutput(t, tm, "Users here:", "alice", "carol")
}

func TestE2E_StartupCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping teatest end-to-end test in -short mode")
	}

	// A zlilyStartup memo whose single line is a plain send. The server prefixes
	// memo content with "* "; the proxy strips it and replays the line, which it
	// forwards upstream as a normal send the fake records on its Commands channel.
	opt := lilytest.DefaultWorld()
	opt.CommandReplies["/memo me zlilyStartup"] = []string{"* STARTUPSEND hi"}

	c, fake := startStackWith(t, opt)

	// The proxy replays the memo once automatically after the login sync.
	fake.WaitCommand(t, "STARTUPSEND hi")

	// %startup re-reads and re-runs the memo on demand, replaying the line again.
	require.NoError(t, c.Send("%startup"))
	fake.WaitCommand(t, "STARTUPSEND hi")
}

func TestE2E_ResizeSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping teatest end-to-end test in -short mode")
	}
	c, fake := startStack(t)
	tm := startUI(t, c)
	waitForOutput(t, tm, "Connected to TestServer")

	fake.Push(lilytest.NotifyLine("public", lilytest.HandleAlice, []string{lilytest.HandleCafe}, "RESIZETOKEN here"))
	waitForOutput(t, tm, "RESIZETOKEN")

	// A live resize (the screen re-attach scenario) must rerender without panic
	// and keep the content.
	tm.Send(tea.WindowSizeMsg{Width: 60, Height: 24})
	waitForOutput(t, tm, "RESIZETOKEN")
}
