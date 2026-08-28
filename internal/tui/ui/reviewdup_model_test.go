package ui

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/require"
)

// reviewLily is a fake Lily whose detach review streams over the WebSocket
// while the proxy's /state call is still blocked on the SLCP sync — the exact
// window in which the live stream and the /events history replay overlap.
type reviewLily struct {
	ln      net.Listener
	reviewN int
	mu      sync.Mutex
	conn    net.Conn
}

func startReviewLily(t *testing.T, reviewN int) *reviewLily {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &reviewLily{ln: ln, reviewN: reviewN}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *reviewLily) write(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conn != nil {
		_, _ = fmt.Fprintf(f.conn, "%s\r\n", line)
	}
}

func (f *reviewLily) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	r := bufio.NewReader(conn)

	f.write("Welcome to lily at TestServer")
	f.write("login:")
	_, _ = r.ReadString('\n') // #$# options
	_, _ = r.ReadString('\n') // credentials
	f.write("%server version=1.0 name=TestServer")
	f.write("%options +version +prompt +prompt2 +leaf-notify +leaf-cmd +connected")
	f.write("*** Connected ***") // login confirmed: /auth returns, TUI dials the WS

	f.write("*** (22:08) ScatterBots has reattached ***")
	f.write("Welcome to lily;                          type /HELP for an introduction")

	// The review streams while /state is still blocked on the sync below.
	f.write("(Beginning review: Tue Aug 11 21:15:08 2026 EDT)")
	for i := 0; i < f.reviewN; i++ {
		f.write(fmt.Sprintf("# - REVIEWLINE %04d", i))
		time.Sleep(time.Millisecond)
	}
	f.write("(End of review)")

	// Only now does the sync complete, unblocking /state.
	f.write("%SLCP-SYNC START")
	f.write("%DATA NAME=whoami VALUE=#100")
	f.write("%USER HANDLE=#100 NAME=me STATE=here")
	f.write("%SLCP-SYNC END")
	f.write("%connected #100")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.Contains(line, "/where"):
			f.write("%begin [1] /where me")
			f.write("You are a member of cafe.")
			f.write("%end [1]")
		case strings.Contains(line, "zlilyStartup"):
			f.write("%begin [2] /memo me zlilyStartup")
			f.write("%end [2]")
		}
	}
}

func startReviewProxy(t *testing.T, lilyAddr string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	srv := api.New(api.Config{LilyAddr: lilyAddr})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.RunWithListener(ctx, l) }()
	t.Cleanup(func() { cancel(); <-errCh })
	return addr
}

// TestReattachReview_RendersEachLineOnce drives the real Model over the real
// proxy through a reattach whose detach review streams while /state is blocked.
// Every review line must land in m.output exactly once.
func TestReattachReview_RendersEachLineOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end review test in -short mode")
	}
	const reviewN = 400

	fake := startReviewLily(t, reviewN)
	c := client.New(startReviewProxy(t, fake.ln.Addr().String()))
	require.NoError(t, c.Auth("alice", "password"))
	require.NoError(t, c.Connect())
	t.Cleanup(c.Close)

	// The real TUI runs a 5-second ReportSeen loop. While the user is parked at
	// -- MORE -- it reports the highest *visible* ID, which lags far behind the
	// streaming review. That non-zero stored last-seen is what makes /state
	// report LastSeenID > 0 and so makes fetchInitialStateCmd actually perform
	// the history fetch -- without it the replay path is never exercised.
	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = c.ReportSeen(2)
	}()

	logChan, _ := NewLogger()
	m := New(c, logChan)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 40))

	// The pager holds at -- MORE -- (exactly as it does for a real reattach), so
	// the connect banner is below the fold and cannot be waited on via the
	// rendered output. Give the live stream and the history replay time to land.
	time.Sleep(8 * time.Second)

	require.NoError(t, tm.Quit())
	final := tm.FinalModel(t, teatest.WithFinalTimeout(10*time.Second)).(Model)

	counts := map[string]int{}
	for _, it := range final.output {
		if s, ok := it.Data.(string); ok {
			counts[s]++
		}
	}

	var dupes []string
	for _, probe := range []string{
		"(Beginning review: Tue Aug 11 21:15:08 2026 EDT)",
		"# - REVIEWLINE 0000",
		fmt.Sprintf("# - REVIEWLINE %04d", reviewN/2),
		fmt.Sprintf("# - REVIEWLINE %04d", reviewN-1),
		"(End of review)",
		"*** (22:08) ScatterBots has reattached ***",
	} {
		t.Logf("  %-52q rendered %d time(s)", probe, counts[probe])
		if counts[probe] != 1 {
			dupes = append(dupes, fmt.Sprintf("%q rendered %d times", probe, counts[probe]))
		}
	}
	t.Logf("total output items: %d (review is %d lines + banners)", len(final.output), reviewN)

	// Confirm the history replay path was actually exercised: without a
	// non-zero stored last-seen, fetchInitialStateCmd skips the fetch entirely
	// and this test proves nothing about dedup.
	t.Logf("storedLastSeenID=%d", final.storedLastSeenID)
	require.NotZero(t, final.storedLastSeenID,
		"history fetch was skipped (state.LastSeenID was 0); the replay path is not exercised")
	// msgMeta is a 100-entry ring, so these counts saturate on a long review and
	// are diagnostics only -- the item count below is the real assertion.
	var skipped, dropped int
	for _, e := range final.msgMeta.entries() {
		if strings.HasPrefix(e.desc, "skip replayed") {
			skipped++
		}
		if strings.HasPrefix(e.desc, "drop dup") {
			dropped++
		}
	}
	t.Logf("dedup activity (ring-capped at %d): %d skipped in replay, %d dropped live",
		msgMetaRingCap, skipped, dropped)

	// Without the dedup (v0.10.1 and earlier) the live stream and the history
	// replay each contribute a full copy, roughly doubling the item count.
	require.Less(t, len(final.output), reviewN+reviewN/2,
		"output roughly doubled: the review was incorporated twice")
	require.Empty(t, dupes, "detach review lines must be incorporated exactly once")
}
