package ui

// Responsiveness metrics: the TUI times how long it takes to handle each
// class of event (keystroke, scroll, incoming proxy event, repaint) and keeps
// the results bucketed by wall-clock window, so a %debug snapshot shows how
// responsiveness has changed across a long session rather than just its
// lifetime average. Sessions that have been up for hours have been reported
// as sluggish; a single average cannot distinguish "always been this slow"
// from "degrades as the session ages", and the trend table can.
//
// Alongside the latencies each window carries a gauge sample (heap, goroutine
// count, scrollback size, bytes written to the terminal) so a latency trend
// can be read against whatever is growing underneath it.
//
// The design constraints are that recording sits on the hot path of every
// keystroke, and that a session may run for weeks:
//
//   - Recording is allocation-free: a duration goes into fixed histogram
//     buckets, never a retained sample list.
//   - Memory is bounded regardless of uptime. When the window ring fills,
//     adjacent windows are merged pairwise and the window duration doubles,
//     so the whole session stays covered at a resolution that halves as it
//     ages. Fixed-edge histograms merge exactly (unlike stored quantiles),
//     which is what makes that coarsening lossless for the numbers reported.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// perfCat is a class of work whose latency is tracked separately.
type perfCat int

const (
	perfKey    perfCat = iota // a keystroke, from press to updated model
	perfScroll                // pager key or mouse wheel
	perfPaste                 // bracketed paste
	perfEvent                 // an incoming message from the proxy
	perfResize                // terminal resize
	perfOther                 // every other Update (ticks, async results)
	perfSync                  // syncViewportContent alone (inside the above)
	perfRender                // View: rendering the frame string
	perfCatCount
)

var perfCatNames = [perfCatCount]string{
	"key", "scroll", "paste", "event", "resize", "other", "sync", "render",
}

// perfBucketEdges are the inclusive upper bounds of the latency histogram.
// They span the range where a TUI's responsiveness is decided: below the
// first edge a keystroke is indistinguishable from instant, above the last
// one the exact figure no longer matters because the session is unusable.
var perfBucketEdges = [...]time.Duration{
	100 * time.Microsecond,
	250 * time.Microsecond,
	500 * time.Microsecond,
	time.Millisecond,
	2 * time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
}

// perfHist is a fixed-bucket latency histogram. counts has one slot per edge
// plus a final overflow slot for anything slower than the last edge.
type perfHist struct {
	counts [len(perfBucketEdges) + 1]uint64
	n      uint64
	sum    time.Duration
	max    time.Duration
}

func (h *perfHist) add(d time.Duration) {
	h.n++
	h.sum += d
	if d > h.max {
		h.max = d
	}
	for i, edge := range perfBucketEdges {
		if d <= edge {
			h.counts[i]++
			return
		}
	}
	h.counts[len(perfBucketEdges)]++
}

func (h *perfHist) merge(o *perfHist) {
	h.n += o.n
	h.sum += o.sum
	if o.max > h.max {
		h.max = o.max
	}
	for i := range h.counts {
		h.counts[i] += o.counts[i]
	}
}

// quantile estimates the q-quantile as the upper edge of the bucket the
// q-th sample falls in, so it always reads as "at or below this". The
// overflow bucket has no upper edge and reports the exact observed max.
func (h *perfHist) quantile(q float64) time.Duration {
	if h.n == 0 {
		return 0
	}
	want := uint64(float64(h.n) * q)
	var seen uint64
	for i, c := range h.counts {
		seen += c
		if seen > want {
			if i == len(perfBucketEdges) {
				return h.max
			}
			return perfBucketEdges[i]
		}
	}
	return h.max
}

func (h *perfHist) mean() time.Duration {
	if h.n == 0 {
		return 0
	}
	return h.sum / time.Duration(h.n)
}

// perfGauge is a point-in-time sample of the things that grow over a session
// and could explain a latency trend.
type perfGauge struct {
	valid        bool
	when         time.Time
	heapAlloc    uint64 // bytes of live heap
	heapObjects  uint64
	heapSys      uint64 // bytes obtained from the OS for the heap
	numGC        uint32
	gcPauseTotal time.Duration
	goroutines   int
	items        int    // scrollback items retained
	lines        int    // rendered lines in the output viewport
	termBytes    uint64 // bytes written to the terminal so far
	termWrites   uint64
}

// perfWindow holds every latency recorded during one slice of the session,
// plus the last gauge sample taken inside it.
type perfWindow struct {
	start time.Time
	end   time.Time // when the window closed (or, for the open one, is due to)
	hists [perfCatCount]perfHist
	gauge perfGauge
}

const (
	// perfMaxWindows bounds the trend table. Once reached, windows are merged
	// pairwise (halving the count, doubling the resolution), so the reported
	// history always spans the whole session using at most this much memory.
	perfMaxWindows = 24

	// perfBaseWindow is the initial trend resolution: fine enough to see a
	// change within a few minutes of it starting, coarse enough that an
	// hours-long session has only coarsened a couple of times.
	perfBaseWindow = time.Minute

	// perfSampleInterval bounds how often gauges are sampled. Sampling calls
	// runtime.ReadMemStats, which stops the world briefly, so it must not
	// happen per event. A window shorter than this (ZLILY_PERF_WINDOW, used
	// when reproducing a slowdown deliberately) tightens it to the window
	// length instead, so every row of the trend table carries its own sample
	// rather than repeating the last one taken.
	perfSampleInterval = 5 * time.Second
)

// busy returns how long the Update loop spent handling messages in this
// window, counting only the top-level categories: sync time is contained
// within them, and render happens in a separate phase.
func (w *perfWindow) busy() time.Duration {
	var total time.Duration
	for _, c := range []perfCat{perfKey, perfScroll, perfPaste, perfEvent, perfResize, perfOther} {
		total += w.hists[c].sum
	}
	return total
}

// busyFraction is that time as a share of the window's wall clock. It is the
// number that connects these latencies to what a user feels: Update is a
// single serialised loop, so a keystroke cannot be handled until whatever the
// loop is already doing finishes. Each individual keystroke can stay fast
// (the key column) while the session still feels unresponsive, because the
// press waits its turn behind a queue of event handling. A window at 90%
// busy has almost no headroom left to answer input promptly.
func (w *perfWindow) busyFraction() float64 {
	wall := w.end.Sub(w.start)
	if wall <= 0 {
		return 0
	}
	// A message whose handling straddled the window boundary is billed to the
	// window it finished in, so a window only a few milliseconds long can
	// report more busy time than it has wall clock. Clamp rather than print a
	// figure over 100%, which would read as a bug in the metric.
	return min(float64(w.busy())/float64(wall), 1)
}

// perfMetrics accumulates the whole session's latency history.
//
// It is held by pointer in Model (like the diagnostic rings) so recording
// survives the value copies Model goes through on every Update, and it is
// mutex-guarded because View may run on a different goroutine than Update.
type perfMetrics struct {
	mu         sync.Mutex
	start      time.Time
	windowDur  time.Duration
	sampleIval time.Duration
	windows    []perfWindow // closed windows, oldest first
	cur        perfWindow   // open window
	total      [perfCatCount]perfHist
	lastGauge  perfGauge
	merges     int // how many times the ring has been compacted
}

// newPerfMetrics starts a metrics collector. The window duration may be
// overridden with ZLILY_PERF_WINDOW (a Go duration) to watch a trend develop
// over seconds rather than minutes when reproducing a slowdown deliberately.
func newPerfMetrics() *perfMetrics {
	dur := perfBaseWindow
	if v := os.Getenv("ZLILY_PERF_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			dur = d
		}
	}
	now := time.Now()
	return &perfMetrics{
		start:      now,
		windowDur:  dur,
		sampleIval: min(dur, perfSampleInterval),
		cur:        perfWindow{start: now, end: now.Add(dur)},
	}
}

// record files one observation. Called on the hot path of every event.
func (p *perfMetrics) record(cat perfCat, d time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rollover(time.Now())
	p.cur.hists[cat].add(d)
	p.total[cat].add(d)
}

// rollover closes the open window if its time is up. A window that expired
// while the session sat idle is closed at its due time and the next one
// starts at now, so an idle gap costs one window rather than one per elapsed
// period.
func (p *perfMetrics) rollover(now time.Time) {
	if now.Before(p.cur.end) {
		return
	}
	p.cur.end = now
	p.windows = append(p.windows, p.cur)
	if len(p.windows) >= perfMaxWindows {
		p.compact()
	}
	p.cur = perfWindow{start: now, end: now.Add(p.windowDur), gauge: p.lastGauge}
}

// compact merges adjacent window pairs and doubles the window duration,
// halving the resolution of the retained history to make room for more.
func (p *perfMetrics) compact() {
	merged := make([]perfWindow, 0, len(p.windows)/2+1)
	for i := 0; i < len(p.windows); i += 2 {
		w := p.windows[i]
		if i+1 < len(p.windows) {
			next := p.windows[i+1]
			for c := range w.hists {
				w.hists[c].merge(&next.hists[c])
			}
			w.end = next.end
			// Keep the later gauge: a window's sample describes where the
			// session had got to by its end.
			if next.gauge.valid {
				w.gauge = next.gauge
			}
		}
		merged = append(merged, w)
	}
	p.windows = merged
	p.windowDur *= 2
	p.merges++
}

// dueForSample reports whether it is time to take another gauge sample.
func (p *perfMetrics) dueForSample(now time.Time) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.lastGauge.valid || now.Sub(p.lastGauge.when) >= p.sampleIval
}

// sample records a gauge reading, filling in the runtime half itself. The
// caller supplies the model-derived fields (see Model.perfGauge).
func (p *perfMetrics) sample(g perfGauge) {
	if p == nil {
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	g.valid = true
	g.when = time.Now()
	g.heapAlloc = ms.HeapAlloc
	g.heapObjects = ms.HeapObjects
	g.heapSys = ms.HeapSys
	g.numGC = ms.NumGC
	g.gcPauseTotal = time.Duration(ms.PauseTotalNs)
	g.goroutines = runtime.NumGoroutine()

	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastGauge = g
	p.cur.gauge = g
}

// ── reporting ─────────────────────────────────────────────────────────────────

// report renders the metrics as text lines: a lifetime latency table, then
// one row per window showing how each class of work and each gauge has moved
// over the session.
func (p *perfMetrics) report() []string {
	if p == nil {
		return []string{"(no metrics collected)"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	uptime := now.Sub(p.start)
	out := []string{
		fmt.Sprintf("uptime=%s resolution=%s windows=%d compactions=%d",
			uptime.Round(time.Second), p.windowDur, len(p.windows)+1, p.merges),
		"latency measured inside the TUI process: from receiving an event to",
		"the updated model (key/scroll/paste/event/resize/other), plus the two",
		"phases they contain or trigger - sync (rebuilding viewport content)",
		"and render (building the frame string). These are handling times, not",
		"the wait a keystroke suffers before its turn: for that, read the busy",
		"column, the share of wall time the single Update loop spent working.",
		"Keys can stay individually fast while a busy loop still feels sluggish.",
		"",
		"lifetime:",
		fmt.Sprintf("  %-8s %9s %9s %9s %9s %9s", "op", "n", "mean", "p50", "p95", "max"),
	}
	for c := perfCat(0); c < perfCatCount; c++ {
		h := &p.total[c]
		if h.n == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("  %-8s %9d %9s %9s %9s %9s",
			perfCatNames[c], h.n, fmtDur(h.mean()), fmtDur(h.quantile(0.50)),
			fmtDur(h.quantile(0.95)), fmtDur(h.max)))
	}

	var busy time.Duration
	for _, c := range []perfCat{perfKey, perfScroll, perfPaste, perfEvent, perfResize, perfOther} {
		busy += p.total[c].sum
	}
	if uptime > 0 {
		out = append(out, fmt.Sprintf("  update loop busy %.1f%% of the session (%s of %s)",
			100*float64(busy)/float64(uptime), busy.Round(time.Millisecond), uptime.Round(time.Second)))
	}

	out = append(out, "",
		"trend (p95 latency per window, oldest first; gauges sampled at window end):",
		fmt.Sprintf("  %-13s %9s %9s %10s %10s %9s %5s  %7s %6s %4s %7s %7s %8s",
			"elapsed", "key", "scroll", "event", "sync", "render", "busy",
			"heap", "objs", "gor", "items", "lines", "termout"))

	windows := append(append([]perfWindow(nil), p.windows...), p.cur)
	for i := range windows {
		w := &windows[i]
		if i == len(windows)-1 {
			// The open window's end is the time it is *due* to close;
			// measuring busy time against that would understate it.
			w.end = now
		}
		out = append(out, fmt.Sprintf("  %-13s %9s %9s %10s %10s %9s %4.0f%%  %7s %6s %4d %7s %7s %8s",
			fmt.Sprintf("%s-%s", fmtElapsed(w.start.Sub(p.start)), fmtElapsed(w.end.Sub(p.start))),
			p95cell(&w.hists[perfKey]),
			p95cell(&w.hists[perfScroll]),
			p95cell(&w.hists[perfEvent]),
			p95cell(&w.hists[perfSync]),
			p95cell(&w.hists[perfRender]),
			100*w.busyFraction(),
			gaugeCell(w.gauge.valid, func() string { return fmtBytes(w.gauge.heapAlloc) }),
			gaugeCell(w.gauge.valid, func() string { return fmtCount(w.gauge.heapObjects) }),
			w.gauge.goroutines,
			gaugeCell(w.gauge.valid, func() string { return fmtCount(uint64(w.gauge.items)) }),
			gaugeCell(w.gauge.valid, func() string { return fmtCount(uint64(w.gauge.lines)) }),
			gaugeCell(w.gauge.valid && w.gauge.termWrites > 0, func() string { return fmtBytes(w.gauge.termBytes) }),
		))
	}
	if g := p.lastGauge; g.valid {
		out = append(out, "",
			fmt.Sprintf("latest gauge: heap=%s (sys %s) objects=%s goroutines=%d gc=%d gcpause=%s",
				fmtBytes(g.heapAlloc), fmtBytes(g.heapSys), fmtCount(g.heapObjects),
				g.goroutines, g.numGC, g.gcPauseTotal.Round(time.Millisecond)),
			fmt.Sprintf("              scrollback=%d items %d lines, terminal out=%s in %s writes",
				g.items, g.lines, fmtBytes(g.termBytes), fmtCount(g.termWrites)))
	}
	return out
}

// p95cell renders one latency cell, showing the sample count alongside the
// p95 so an empty-looking window can be told from a genuinely fast one.
func p95cell(h *perfHist) string {
	if h.n == 0 {
		return "-"
	}
	return fmt.Sprintf("%s/%s", fmtCount(h.n), fmtDur(h.quantile(0.95)))
}

func gaugeCell(valid bool, f func() string) string {
	if !valid {
		return "-"
	}
	return f()
}

// fmtDur renders a latency at a fixed three significant figures or so, which
// keeps the trend columns aligned and comparable at a glance.
func fmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fus", float64(d)/float64(time.Microsecond))
	case d < 10*time.Millisecond:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	case d < time.Second:
		return fmt.Sprintf("%.0fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

func fmtElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

func fmtBytes(b uint64) string {
	switch {
	case b < 1<<10:
		return fmt.Sprintf("%dB", b)
	case b < 1<<20:
		return fmt.Sprintf("%.0fK", float64(b)/(1<<10))
	case b < 1<<30:
		return fmt.Sprintf("%.1fM", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%.2fG", float64(b)/(1<<30))
	}
}

func fmtCount(n uint64) string {
	switch {
	case n < 10000:
		return fmt.Sprint(n)
	case n < 10_000_000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprintf("%dM", n/1_000_000)
	}
}

// ── model wiring ──────────────────────────────────────────────────────────────

// perfCategory classifies a message for latency accounting.
//
// Scroll keys are split out from ordinary typing because the two are what
// users describe separately when they call the client sluggish, and because
// they exercise different code (viewport offset math vs. input re-render).
// HalfPageUp/HalfPageDown are deliberately not counted as scrolling: C-u and
// C-d are also kill-to-start and quit-on-empty-input, so which one a press
// meant is not knowable from the key alone.
func (m Model) perfCategory(msg tea.Msg) perfCat {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if !m.authMode && !m.editMode && !m.searchMode &&
			key.Matches(msg, m.keys.PageUp, m.keys.PageDown, m.keys.ScrollUp,
				m.keys.ScrollDown, m.keys.GotoTop, m.keys.GotoBottom) {
			return perfScroll
		}
		return perfKey
	case tea.PasteMsg:
		return perfPaste
	case tea.MouseWheelMsg:
		return perfScroll
	case serverEventMsg:
		return perfEvent
	case initialStateMsg:
		return perfEvent
	case tea.WindowSizeMsg:
		return perfResize
	}
	return perfOther
}

// perfGauge collects the model-side half of a gauge sample. Every field is
// O(1) to read, so sampling costs nothing beyond the ReadMemStats in
// perfMetrics.sample.
func (m Model) perfGauge() perfGauge {
	g := perfGauge{
		items: len(m.output),
		lines: m.viewport.TotalLineCount(),
	}
	if c, ok := m.rendererTap.(interface{ Written() (uint64, uint64) }); ok {
		g.termWrites, g.termBytes = c.Written()
	}
	return g
}

// PerfReport returns the responsiveness metrics as text. Exported for
// out-of-package harnesses that drive the model and want the numbers without
// writing a snapshot file (see internal/integration).
func (m Model) PerfReport() string {
	return strings.Join(m.perf.report(), "\n")
}
