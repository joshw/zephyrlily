package integration

import (
	"bytes"
	"context"
	"fmt"
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
	c := client.New(startProxy(t, fake))
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)
	return c, fake
}

// startProxy boots a real proxy on an ephemeral loopback port pointing at
// fake and returns its address. Shut down via t.Cleanup.
func startProxy(t *testing.T, fake *lilytest.Server) string {
	t.Helper()
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
	return proxyAddr
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

// TestE2E_InterleavedCommandLeafing drives two concurrent leafed commands — the
// shape of /review commands replayed from zlilyStartup while the attach-time
// review is still streaming — and verifies each %command-tagged line lands only
// in its own command's result, with the wire prefix stripped.
func TestE2E_InterleavedCommandLeafing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end test in -short mode")
	}
	c, fake := startStack(t)

	fake.Push(
		"%begin [101] /review detach",
		"%begin [202] /review discussion",
		"%command [202] REVIEWB no matching events",
		"%command [101] REVIEWA beginning review",
		"%command [101] # REVIEWA from clee",
		"%end [202]",
		"%end [101]",
	)

	results := map[int][]string{}
	deadline := time.After(5 * time.Second)
	for len(results) < 2 {
		select {
		case msg, ok := <-c.Events:
			require.True(t, ok, "events channel closed before both results arrived")
			d, _ := msg.Data.(map[string]interface{})
			switch msg.Type {
			case "commandresult":
				id, _ := d["cmd_id"].(float64)
				if int(id) != 101 && int(id) != 202 {
					continue // unrelated capture (e.g. the login /where)
				}
				lines := []string{}
				for _, l := range d["lines"].([]interface{}) {
					lines = append(lines, l.(string))
				}
				results[int(id)] = lines
			case "text":
				// No tagged line may leak out of the captures as plain text.
				text, _ := d["text"].(string)
				require.NotContains(t, text, "REVIEW")
			}
		case <-deadline:
			t.Fatalf("timed out waiting for command results; got %v", results)
		}
	}

	require.Equal(t, []string{"REVIEWB no matching events"}, results[202])
	require.Equal(t, []string{"REVIEWA beginning review", "# REVIEWA from clee"}, results[101])
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

// A fresh WebSocket subscriber must receive the proxy's entire buffered event
// ring. Before the per-client outbound queue, handleWS pushed the ring into a
// fixed 64-slot channel with a non-blocking send, silently dropping everything
// past the cap — an attach-time review burst lost most of its lines (blank
// separators and wrapped continuations included) with no trace in any log.
func TestE2E_BufferedBurstDeliveredCompletely(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end test in -short mode")
	}
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	c := client.New(startProxy(t, fake))
	require.NoError(t, c.Auth("alice", "password"))

	// Burst arrives while no WebSocket subscriber is connected, so every line
	// lands in the proxy's ring buffer (well past the old 64-message cap).
	const n = 500
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("# BURST line %04d", i)
	}
	fake.Push(lines...)
	require.Eventually(t, func() bool {
		st, err := c.FetchState()
		return err == nil && st.EventBufSize >= n
	}, 5*time.Second, 20*time.Millisecond, "proxy never buffered the burst")

	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)

	seen := make(map[int]bool)
	deadline := time.After(10 * time.Second)
	for len(seen) < n {
		select {
		case msg, ok := <-c.Events:
			require.True(t, ok, "events channel closed after %d/%d burst lines", len(seen), n)
			if msg.Type != "text" {
				continue
			}
			d, _ := msg.Data.(map[string]interface{})
			text, _ := d["text"].(string)
			var i int
			if _, err := fmt.Sscanf(text, "# BURST line %d", &i); err == nil {
				seen[i] = true
			}
		case <-deadline:
			t.Fatalf("timed out: received %d of %d burst lines", len(seen), n)
		}
	}
}
