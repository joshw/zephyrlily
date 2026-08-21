package integration

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/tui/ui"
	"github.com/stretchr/testify/require"
)

// Load harness for the "zlily gets sluggish after a long session" reports.
//
// It drives the real stack — fake Lily → proxy → WebSocket → TUI model — with
// a sustained stream of simulated events while typing and scrolling
// throughout, then prints the TUI's own responsiveness metrics
// (internal/tui/ui/perf.go). The point is to age a session in seconds instead
// of days: the trend table's rows are windows of that aging, so a slowdown
// that only appears once the scrollback has grown shows up as climbing p95
// columns, with the gauge columns naming whatever grew alongside it.
//
// The knobs are environment variables so a run can be stretched without
// editing code:
//
//	ZLILY_LOAD_SECONDS=120 go test ./internal/integration -run TestPerfLoad -v
//	ZLILY_LOAD_RATE=500    events pushed per second (default 400)
//	ZLILY_LOAD_WINDOW=5s   trend resolution (default 2s)
//
// The default rate is above what the TUI sustains once its scrollback has
// grown, which is what makes a 20-second run show the slowdown at all. Runs
// longer than about a minute at that rate end with the proxy dropping the TUI
// as a slow consumer (see reconnectPrompt below); lower the rate for those.
//
// Two structural details matter for reading the numbers. Events reach the
// model one per Update cycle (listenCmd re-arms after each), while messages
// from tm.Send jump straight onto the queue — so keystrokes must be spread
// across the whole run, not batched with the pushes, or they all land in the
// first seconds while the scrollback is still small. And key presses are sent
// as messages rather than typed into teatest's input buffer, which is a plain
// bytes.Buffer whose reader is not a reliable path once it has been drained.

// loadEnvInt reads a positive integer knob, falling back to def.
func loadEnvInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	require.NoErrorf(t, err, "%s must be an integer", name)
	require.Positivef(t, n, "%s must be positive", name)
	return n
}

// reconnectPrompt is what the TUI shows once its event stream has dropped.
// The proxy disconnects a WebSocket client that falls maxClientBacklog behind
// (by design: it would rather the client refetch than be fed a stream with
// invisible gaps), so pushing faster than the TUI can consume eventually ends
// the run's event flow. That is worth reporting rather than silently
// measuring an idle session for the remaining windows.
const reconnectPrompt = "Reconnect? (Y/n)"

// drain consumes the TUI's rendered frames in the background. teatest's
// output is an unbounded in-memory buffer; left unread it would grow to
// hundreds of megabytes over a long run and show up in the very heap gauge
// this harness exists to read. It counts bytes as it goes (an independent
// check on terminal-output volume) and notes when the reconnect prompt first
// appears.
type drain struct {
	bytes     atomic.Uint64
	droppedAt atomic.Int64 // unix nanos of the first reconnect prompt, 0 if none
	start     time.Time
	carry     []byte // tail kept so a prompt split across reads still matches
}

func drainOutput(r io.Reader, stop <-chan struct{}) *drain {
	d := &drain{start: time.Now()}
	go func() {
		buf := make([]byte, 64*1024)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := r.Read(buf)
			d.bytes.Add(uint64(n))
			if n > 0 && d.droppedAt.Load() == 0 {
				scan := append(d.carry, buf[:n]...)
				if bytes.Contains(scan, []byte(reconnectPrompt)) {
					d.droppedAt.Store(time.Now().UnixNano())
				}
				if keep := len(reconnectPrompt); len(scan) > keep {
					d.carry = append(d.carry[:0], scan[len(scan)-keep:]...)
				} else {
					d.carry = scan
				}
			}
			if err != nil || n == 0 {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()
	return d
}

// dropped reports how far into the run the event stream died, if it did.
func (d *drain) dropped() (time.Duration, bool) {
	ns := d.droppedAt.Load()
	if ns == 0 {
		return 0, false
	}
	return time.Unix(0, ns).Sub(d.start), true
}

func TestPerfLoad_SustainedEventStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping load harness in -short mode")
	}

	// The default run is short enough to sit in the normal suite; it still
	// shows the trend, but a slowdown worth reading is better watched over a
	// minute or more via ZLILY_LOAD_SECONDS.
	seconds := loadEnvInt(t, "ZLILY_LOAD_SECONDS", 10)
	rate := loadEnvInt(t, "ZLILY_LOAD_RATE", 400)
	window := "2s"
	if v := os.Getenv("ZLILY_LOAD_WINDOW"); v != "" {
		window = v
	}
	// Read by newPerfMetrics when ui.New builds the model below, so the trend
	// resolves at the timescale of this run rather than the default minute.
	t.Setenv("ZLILY_PERF_WINDOW", window)

	c, fake := startStack(t)
	tm := startUI(t, c)
	waitForOutput(t, tm, "Connected to TestServer")

	stop := make(chan struct{})
	defer close(stop)
	out := drainOutput(tm.Output(), stop)

	duration := time.Duration(seconds) * time.Second
	deadline := time.Now().Add(duration)

	// Push events from their own goroutine: writes to the fake server block
	// once the stack is saturated, and that backpressure must not also stall
	// the typing that is being measured.
	pushed := make(chan int, 1)
	go func() {
		const batch = 20
		n := 0
		tick := time.NewTicker(time.Second / time.Duration(max(rate/batch, 1)))
		defer tick.Stop()
		for time.Now().Before(deadline) {
			select {
			case <-tick.C:
			case <-stop:
				pushed <- n
				return
			}
			lines := make([]string, 0, batch)
			for i := 0; i < batch; i++ {
				lines = append(lines, lilytest.NotifyLine("public", lilytest.HandleAlice,
					[]string{lilytest.HandleCafe},
					fmt.Sprintf("LOAD %d lorem ipsum dolor sit amet consectetur adipiscing elit", n+i)))
			}
			fake.Push(lines...)
			n += batch
		}
		pushed <- n
	}()

	// Interleave the two things users call sluggish, spread evenly over the
	// run. Typing goes through the input-line path, the pager keys through the
	// viewport path. Nothing is submitted: a submit would round-trip to the
	// fake server and measure its latency rather than the TUI's.
	typed := 0
	for time.Now().Before(deadline) {
		tm.Send(tea.KeyPressMsg{Code: 'h', Text: "h"})
		tm.Send(tea.KeyPressMsg{Code: 'i', Text: "i"})
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
		tm.Send(tea.KeyPressMsg{Code: tea.KeyPgUp})
		tm.Send(tea.KeyPressMsg{Code: tea.KeyPgDown})
		typed += 6
		time.Sleep(100 * time.Millisecond)
	}

	// Let the tail of the stream arrive before freezing the model.
	time.Sleep(2 * time.Second)
	require.NoError(t, tm.Quit())
	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(30*time.Second)).(ui.Model)
	require.True(t, ok, "final model should be a ui.Model")

	// The pusher can still be blocked inside a write when the run ends (the
	// stack applies backpressure all the way back to the fake server), so its
	// count is reported if it arrives and skipped if it does not, rather than
	// hanging the harness on a number that is only informational.
	count := -1
	select {
	case count = <-pushed:
	case <-time.After(5 * time.Second):
	}
	t.Logf("ran %s at %d events/s: pushed %d events, sent %d key messages, drained %d bytes of frames",
		duration, rate, count, typed, out.bytes.Load())
	if when, ok := out.dropped(); ok {
		t.Logf("NOTE: the event stream ended %s in - the proxy dropped the TUI as a "+
			"slow consumer, so later windows measure an idle session. Lower "+
			"ZLILY_LOAD_RATE to keep a long run measurable.", when.Round(time.Second))
	}
	t.Logf("\n%s", final.PerfReport())
}
