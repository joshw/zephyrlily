package ui

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/require"
)

// reviewPromptText is the prompt Lily gates the detach review behind.
const reviewPromptText = "You were detached, do you wish to review now? (Y/n)"

// reviewBody is the review from the incident report, verbatim.
var reviewBody = []string{
	"(Beginning review: Thu Jun 25 09:28:02 2026 EDT)",
	"# *** Sue D. Nymme has changed his blurb to [hol'up] ***",
	"# *** (09:28) Sue D. Nymme [hol'up] has detached ***",
	"# *** (09:28) Sue D. Nymme has reattached ***",
	"(End of review)",
}

// promptLily is a fake Lily that gates its detach review behind a
// "%prompt … review now?", the way the real server does on reattach. Both the
// prompt and the review it unlocks are emitted while the SLCP sync — and so the
// TUI's /state call — is still blocked, which is what makes the history replay
// that follows span the prompt. Faithfully to the incident report, the server
// never sends a follow-up %prompt to retire it: nothing but the client can.
//
// awaitAnswer picks the scenario. True: hold the review until the user answers,
// having first given the TUI's WebSocket time to attach, so the prompt is
// delivered live and answered. False: emit the prompt immediately after login
// and never wait, so a WebSocket that is still dialling misses it entirely and
// the prompt exists only in the proxy's event buffer.
type promptLily struct {
	ln          net.Listener
	awaitAnswer bool
	mu          sync.Mutex
	conn        net.Conn
}

func startPromptLily(t *testing.T, awaitAnswer bool) *promptLily {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &promptLily{ln: ln, awaitAnswer: awaitAnswer}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *promptLily) write(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		_, _ = fmt.Fprintf(f.conn, "%s\r\n", line)
	}
}

func (f *promptLily) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	r := bufio.NewReader(conn)

	f.write("Welcome to lily at RPI")
	f.write("login:")
	_, _ = r.ReadString('\n') // #$# options
	_, _ = r.ReadString('\n') // credentials
	f.write("%server version=1.0 name=RPI")
	f.write("%options +version +prompt +prompt2 +leaf-notify +leaf-cmd +connected")
	f.write("*** Connected ***") // login confirmed: /auth returns, TUI dials the WS

	f.write("*** (09:28) Sue D. Nymme has reattached ***")

	if f.awaitAnswer {
		// Let the WebSocket attach first, so the prompt is delivered live
		// exactly as it was for the user who answered it.
		time.Sleep(500 * time.Millisecond)
	}
	f.write("%prompt " + reviewPromptText)

	if f.awaitAnswer {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if f.respondToStartup(line) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(line), "Y") {
				break
			}
		}
		for _, l := range reviewBody {
			f.write(l)
		}
	} else {
		time.Sleep(1500 * time.Millisecond)
	}

	// Only now does the sync complete, unblocking /state — so the replay that
	// follows spans the prompt (and, above, the whole review).
	f.write("%SLCP-SYNC START")
	f.write("%DATA NAME=whoami VALUE=#829")
	f.write("%USER HANDLE=#829 NAME=Sue_D._Nymme STATE=here")
	f.write("%SLCP-SYNC END")
	f.write("%connected #829")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f.respondToStartup(line)
	}
}

// respondToStartup answers the probes the proxy issues on its own behalf,
// reporting whether the line was one of them.
func (f *promptLily) respondToStartup(line string) bool {
	switch {
	case strings.Contains(line, "/where"):
		f.write("%begin [1] /where me")
		f.write("You are a member of cafe.")
		f.write("%end [1]")
		return true
	case strings.Contains(line, "zlilyStartup"):
		f.write("%begin [2] /memo me zlilyStartup")
		f.write("%end [2]")
		return true
	}
	return false
}

// promptMsgMeta returns the model's diagnostic ring as a slice, logging it.
func promptMsgMeta(t *testing.T, m Model) []string {
	t.Helper()
	var out []string
	for _, e := range m.msgMeta.entries() {
		t.Logf("  msgMeta: %s", e.desc)
		out = append(out, e.desc)
	}
	return out
}

func hasPrefixIn(entries []string, prefixes ...string) bool {
	for _, e := range entries {
		for _, p := range prefixes {
			if strings.HasPrefix(e, p) {
				return true
			}
		}
	}
	return false
}

// TestReattachReviewPrompt_RetiredOnceAnswered reproduces the report that zlily
// "asks you twice whether you want to review": the second ask was a stale prompt
// string left in the input area, not a real prompt (typing at it was treated as
// ordinary input). Answering clears it locally; the history replay that lands
// afterwards must not resurrect it, and neither must the /state snapshot.
func TestReattachReviewPrompt_RetiredOnceAnswered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end review-prompt test in -short mode")
	}

	fake := startPromptLily(t, true)
	c := client.New(startReviewProxy(t, fake.ln.Addr().String()))
	require.NoError(t, c.Auth("sdn", "password"))
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)

	// A non-zero stored last-seen is what makes /state report LastSeenID > 0 and
	// so makes fetchInitialStateCmd actually perform the history fetch; without
	// it the replay path is never exercised and the test proves nothing.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = c.ReportSeen(1)
	}()

	logChan, _ := NewLogger()
	m := New(c, logChan)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 40))

	// Wait for the prompt to reach the input area, then answer it as the user did.
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "do you wish to review now?")
	}, teatest.WithDuration(10*time.Second))

	tm.Send(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Let the review and the late history replay land.
	time.Sleep(5 * time.Second)

	require.NoError(t, tm.Quit())
	final := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(Model)

	// The replay path must actually have run, and must actually have carried the
	// prompt — otherwise the assertions below are vacuous.
	t.Logf("storedLastSeenID=%d", final.storedLastSeenID)
	require.NotZero(t, final.storedLastSeenID,
		"history fetch was skipped (state.LastSeenID was 0); the replay path is not exercised")

	entries := promptMsgMeta(t, final)
	// Either suppression path counts: the ID dedup catches the replayed prompt
	// when the session's message IDs are continuous, and the replay-time
	// "prompts are live state" guard catches it when they are not.
	require.True(t, hasPrefixIn(entries,
		"skip replayed prompt", "skip replayed type=prompt", "drop dup type=prompt"),
		"the history replay never re-delivered the prompt; this test does not cover the reported path")
	require.False(t, hasPrefixIn(entries, "apply state prompt"),
		"an answered prompt must not be re-applied from the /state snapshot")

	require.Equal(t, "", final.prompt,
		"answered review prompt must not be left in the input area")
	require.Equal(t, "", final.inputPromptText(),
		"input area must render no prompt once the review prompt is answered")

	// And the review it unlocked must appear exactly once.
	counts := map[string]int{}
	for _, it := range final.output {
		if s, ok := it.Data.(string); ok {
			counts[s]++
		}
	}
	for _, probe := range reviewBody {
		t.Logf("  %-60q rendered %d time(s)", probe, counts[probe])
		require.Equalf(t, 1, counts[probe], "%q rendered %d times", probe, counts[probe])
	}
}

// TestReattachReviewPrompt_ShownWhenOnlyInHistory covers the other side of the
// same coin. If the TUI's WebSocket is still dialling when Lily emits the
// prompt, the prompt reaches only the proxy's event buffer — and clients ignore
// prompts in the /events replay, since a replayed prompt has normally been
// answered already. An unanswered one must still surface, via the /state
// snapshot, or the user is left staring at a blank input area while login sits
// blocked on a question they were never shown.
func TestReattachReviewPrompt_ShownWhenOnlyInHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end review-prompt test in -short mode")
	}

	fake := startPromptLily(t, false)
	c := client.New(startReviewProxy(t, fake.ln.Addr().String()))
	require.NoError(t, c.Auth("sdn", "password"))
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = c.ReportSeen(1)
	}()
	// The prompt is already on the wire; this dial deliberately loses the race.
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)

	logChan, _ := NewLogger()
	m := New(c, logChan)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 40))
	time.Sleep(5 * time.Second)

	require.NoError(t, tm.Quit())
	final := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(Model)

	t.Logf("storedLastSeenID=%d", final.storedLastSeenID)
	entries := promptMsgMeta(t, final)
	require.True(t, hasPrefixIn(entries, "skip replayed prompt"),
		"the prompt reached this client live after all; the missed-prompt path is not exercised")

	require.Equal(t, reviewPromptText, final.prompt,
		"an unanswered prompt the WebSocket missed must be recovered from /state")
	require.Equal(t, reviewPromptText, final.inputPromptText(),
		"the recovered prompt must render in the input area")
}
