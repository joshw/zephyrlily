package lily

import (
	"errors"
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/slcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectSynced dials a real Conn at the fake server and blocks until the
// initial sync completes.
func connectSynced(t *testing.T, fake *lilytest.Server) *Conn {
	t.Helper()
	conn := NewConn(fake.Addr(), "alice", "password", false, false)
	require.NoError(t, conn.Connect())
	t.Cleanup(conn.Close)
	select {
	case <-conn.SyncComplete():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SyncComplete")
	}
	return conn
}

// waitNotify drains Events until a %NOTIFY of the given event type arrives.
func waitNotify(t *testing.T, conn *Conn, event string) *slcp.Message {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg, ok := <-conn.Events:
			if !ok {
				t.Fatalf("Events closed before %q notify arrived", event)
			}
			if msg.Type == slcp.MsgNotify && msg.Params["EVENT"] == event {
				return msg
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q notify", event)
		}
	}
}

func TestConn_HandshakeAndSync(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	conn := connectSynced(t, fake)

	st := conn.State()
	assert.Equal(t, lilytest.HandleMe, st.Whoami)
	assert.Equal(t, "TestServer", st.Name)

	alice := st.LookupName("alice")
	require.NotNil(t, alice)
	assert.Equal(t, lilytest.HandleAlice, alice.Handle)
	assert.Equal(t, "here", alice.State)

	bob := st.LookupHandle(lilytest.HandleBob)
	require.NotNil(t, bob)
	assert.Equal(t, "bob", bob.Name)
	assert.Equal(t, "away", bob.State)
	assert.Equal(t, "busy", bob.Blurb)

	carol := st.LookupHandle(lilytest.HandleCarol)
	require.NotNil(t, carol)
	assert.Equal(t, "they", carol.Pronoun)

	cafe := st.LookupName("cafe")
	require.NotNil(t, cafe)
	assert.Equal(t, "The Cafe", cafe.Title)

	// Membership reflects the /where response: cafe + lobby, not sandbox.
	assert.True(t, st.IsDiscMember(lilytest.HandleCafe))
	assert.True(t, st.IsDiscMember(lilytest.HandleLobby))
	assert.False(t, st.IsDiscMember(lilytest.HandleSand))
}

func TestConn_LiveEvents_PublicPrivateEmote(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	conn := connectSynced(t, fake)

	fake.Push(
		lilytest.NotifyLine("public", lilytest.HandleAlice, []string{lilytest.HandleCafe}, "hello cafe"),
		lilytest.NotifyLine("private", lilytest.HandleBob, []string{lilytest.HandleMe}, "psst"),
		lilytest.NotifyLine("emote", lilytest.HandleCarol, []string{lilytest.HandleCafe}, " waves"),
	)

	pub := waitNotify(t, conn, "public")
	assert.Equal(t, lilytest.HandleAlice, pub.Params["SOURCE"])
	assert.Equal(t, lilytest.HandleCafe, pub.Params["RECIPS"])
	assert.Equal(t, "hello cafe", pub.Params["VALUE"])

	priv := waitNotify(t, conn, "private")
	assert.Equal(t, lilytest.HandleBob, priv.Params["SOURCE"])
	assert.Equal(t, "psst", priv.Params["VALUE"])

	emote := waitNotify(t, conn, "emote")
	assert.Equal(t, lilytest.HandleCarol, emote.Params["SOURCE"])
	assert.Equal(t, " waves", emote.Params["VALUE"])
}

func TestConn_SendRoundTrip(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	conn := connectSynced(t, fake)

	require.NoError(t, conn.Send("/who"))
	fake.WaitCommand(t, "/who")

	// The leafed command output flows back as %begin / raw lines / %end.
	var raw []string
	sawBegin := false
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case msg, ok := <-conn.Events:
			require.True(t, ok, "Events closed before command output completed")
			switch msg.Type {
			case slcp.MsgCmdBegin:
				if msg.Text == "/who" {
					sawBegin = true
				}
			case slcp.MsgRaw:
				raw = append(raw, msg.Text)
			case slcp.MsgCmdEnd:
				if sawBegin {
					break loop
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for command output")
		}
	}

	assert.True(t, sawBegin, "expected a %%begin for /who")
	assert.Contains(t, raw, "Users here:")
	assert.Contains(t, raw, "  alice")
}

func TestConn_OptionsValidationFailure(t *testing.T) {
	opt := lilytest.DefaultWorld()
	opt.OmitOptions = []string{"+leaf-notify"}
	fake := lilytest.Start(t, opt)

	conn := NewConn(fake.Addr(), "alice", "password", false, false)
	t.Cleanup(conn.Close)
	err := conn.Connect()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support required options")
}

func TestConn_LoginRejected(t *testing.T) {
	opt := lilytest.DefaultWorld()
	opt.RejectLogin = true
	fake := lilytest.Start(t, opt)

	conn := NewConn(fake.Addr(), "alice", "wrongpass", false, false)
	t.Cleanup(conn.Close)

	done := make(chan error, 1)
	go func() { done <- conn.Connect() }()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrAuthFailed), "expected ErrAuthFailed, got %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Connect() hung on rejected login instead of returning an error")
	}
}

func TestConn_CloseClosesEvents(t *testing.T) {
	fake := lilytest.Start(t, lilytest.DefaultWorld())
	conn := connectSynced(t, fake)

	conn.Close()

	done := make(chan struct{})
	go func() {
		for range conn.Events { //nolint:revive // draining until closed
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Events channel was not closed after Close()")
	}
}
