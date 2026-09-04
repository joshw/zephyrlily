package integration

import (
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/require"
)

// collect drains events until one satisfies match, or gives up.
func collect(t *testing.T, c *client.Client, match func(*api.WSServerMsg) bool) *api.WSServerMsg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-c.Events:
			if ev == nil {
				t.Fatal("events channel closed")
			}
			if match(ev) {
				return ev
			}
		case <-deadline:
			return nil
		}
	}
}

func isInputEcho(text string) func(*api.WSServerMsg) bool {
	return func(ev *api.WSServerMsg) bool {
		if ev.Type != "input" {
			return false
		}
		d, ok := ev.Data.(map[string]any)
		if !ok {
			return false
		}
		return d["text"] == text
	}
}

// A session can have several clients attached, and Lily's acknowledgement of a
// sent message reaches all of them. The line itself has to as well: echoing it
// only where it was typed left every other client showing an acknowledgement
// for a message it never saw.
func TestE2E_SentLineIsEchoedToEveryClient(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	sender := client.New(proxyAddr)
	require.NoError(t, sender.Auth("alice", "password"))
	t.Cleanup(sender.Close)
	require.NoError(t, sender.Connect())

	// A second client on the same session, as a second terminal would be.
	watcher := client.New(proxyAddr)
	require.NoError(t, watcher.Auth("alice", "password"))
	t.Cleanup(watcher.Close)
	require.NoError(t, watcher.Connect())
	require.Equal(t, sender.Token(), watcher.Token(), "both should share one session")

	const line = ";alice hello from the other terminal"
	require.NoError(t, sender.Send(line))

	onSender := collect(t, sender, isInputEcho(line))
	require.NotNil(t, onSender, "the sending client never saw its own line echoed")

	onWatcher := collect(t, watcher, isInputEcho(line))
	require.NotNil(t, onWatcher, "the other client never saw the line, only the acknowledgement")

	// The same item, not two independently invented ones: the ID is what the
	// TUI deduplicates and orders scrollback by.
	require.Equal(t, onSender.ID, onWatcher.ID, "the echo should be one event, seen by both")
}

// The echo is part of the scrollback, so a client that connects afterwards must
// see it in the replay rather than an acknowledgement with nothing above it.
func TestE2E_EchoSurvivesForALaterClient(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	sender := client.New(proxyAddr)
	require.NoError(t, sender.Auth("alice", "password"))
	t.Cleanup(sender.Close)
	require.NoError(t, sender.Connect())

	const line = ";alice sent before you arrived"
	require.NoError(t, sender.Send(line))
	require.NotNil(t, collect(t, sender, isInputEcho(line)))

	// Now attach, and read the buffered history.
	late := client.New(proxyAddr)
	require.NoError(t, late.Auth("alice", "password"))
	t.Cleanup(late.Close)
	require.NoError(t, late.Connect())

	require.NotNil(t, collect(t, late, isInputEcho(line)),
		"a client attaching later did not get the echo in its replay")
}
