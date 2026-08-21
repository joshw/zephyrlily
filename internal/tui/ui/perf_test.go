package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerfHistQuantilesAndMax(t *testing.T) {
	var h perfHist
	// 90 fast samples and 10 slow ones: the median must stay in the fast
	// bucket while p95 lands in the slow one.
	for i := 0; i < 90; i++ {
		h.add(200 * time.Microsecond)
	}
	for i := 0; i < 10; i++ {
		h.add(300 * time.Millisecond)
	}

	assert.Equal(t, uint64(100), h.n)
	assert.Equal(t, 250*time.Microsecond, h.quantile(0.50), "p50 reports its bucket's upper edge")
	assert.Equal(t, 500*time.Millisecond, h.quantile(0.95))
	assert.Equal(t, 300*time.Millisecond, h.max, "max is exact, not bucketed")
}

func TestPerfHistOverflowBucketReportsMax(t *testing.T) {
	var h perfHist
	h.add(9 * time.Second)
	// Nothing above the last edge has an upper bound to report, so the
	// observed max stands in for it rather than a meaningless ">1s".
	assert.Equal(t, 9*time.Second, h.quantile(0.95))
}

func TestPerfHistMergeIsExact(t *testing.T) {
	var a, b, both perfHist
	for i := 0; i < 50; i++ {
		a.add(time.Duration(i) * time.Millisecond)
		both.add(time.Duration(i) * time.Millisecond)
	}
	for i := 0; i < 30; i++ {
		b.add(time.Duration(i) * 3 * time.Millisecond)
		both.add(time.Duration(i) * 3 * time.Millisecond)
	}
	a.merge(&b)

	// Merging is what lets the window ring coarsen without distorting the
	// numbers, so it must agree exactly with recording into one histogram.
	assert.Equal(t, both.n, a.n)
	assert.Equal(t, both.sum, a.sum)
	assert.Equal(t, both.max, a.max)
	assert.Equal(t, both.counts, a.counts)
	assert.Equal(t, both.quantile(0.95), a.quantile(0.95))
}

func TestPerfMetricsWindowsStayBoundedAndCoverSession(t *testing.T) {
	p := &perfMetrics{
		start:      time.Now(),
		windowDur:  time.Millisecond,
		sampleIval: time.Millisecond,
		cur:        perfWindow{start: time.Now(), end: time.Now()},
	}
	// Far more windows than the ring holds: memory must stay bounded while
	// the history still reaches back to the first sample.
	for i := 0; i < 20*perfMaxWindows; i++ {
		p.record(perfKey, time.Millisecond)
		p.cur.end = time.Now() // force the next record to roll the window over
	}

	require.Less(t, len(p.windows), perfMaxWindows)
	assert.Greater(t, p.merges, 0, "the ring should have compacted")
	assert.Greater(t, p.windowDur, time.Millisecond, "compaction doubles the resolution")

	// No observation may be lost to compaction: the windows plus the open one
	// must still account for every recorded sample.
	var n uint64
	for i := range p.windows {
		n += p.windows[i].hists[perfKey].n
	}
	n += p.cur.hists[perfKey].n
	assert.Equal(t, uint64(20*perfMaxWindows), n)
	assert.Equal(t, uint64(20*perfMaxWindows), p.total[perfKey].n)
}

func TestPerfReportShowsTrendAndGauges(t *testing.T) {
	p := newPerfMetrics()
	p.windowDur = time.Millisecond

	// A fast early window, then a slow later one - the shape a degrading
	// session has, and the reason the report is a trend rather than a mean.
	for i := 0; i < 50; i++ {
		p.record(perfKey, 200*time.Microsecond)
	}
	p.sample(perfGauge{items: 100, lines: 200})
	p.cur.end = time.Now()
	for i := 0; i < 50; i++ {
		p.record(perfKey, 300*time.Millisecond)
	}
	p.sample(perfGauge{items: 9000, lines: 40000})

	report := strings.Join(p.report(), "\n")
	assert.Contains(t, report, "lifetime:")
	assert.Contains(t, report, "trend (p95 latency per window")
	assert.Contains(t, report, "key")
	// Both regimes must be visible as separate rows, not averaged together.
	assert.Contains(t, report, "50/250us")
	assert.Contains(t, report, "50/500ms")
	assert.Contains(t, report, "9000", "the gauge sample rides along with the latencies")
	assert.Contains(t, report, "latest gauge:")
}

func TestPerfMetricsNilIsInert(t *testing.T) {
	// Models built by tests that bypass New must not panic on the hot path.
	var p *perfMetrics
	assert.NotPanics(t, func() {
		p.record(perfKey, time.Millisecond)
		p.sample(perfGauge{})
		assert.False(t, p.dueForSample(time.Now()))
		assert.NotEmpty(t, p.report())
	})
}

func TestPerfSamplingIsRateLimited(t *testing.T) {
	p := newPerfMetrics()
	assert.True(t, p.dueForSample(time.Now()), "first sample is always due")
	p.sample(perfGauge{items: 1})
	assert.False(t, p.dueForSample(time.Now()), "ReadMemStats must not run per event")
	assert.True(t, p.dueForSample(time.Now().Add(perfSampleInterval)))
}

func TestPerfWindowBusyFraction(t *testing.T) {
	start := time.Now()
	w := perfWindow{start: start, end: start.Add(time.Second)}
	for i := 0; i < 4; i++ {
		w.hists[perfEvent].add(200 * time.Millisecond)
	}
	// sync is contained inside the event handling above; counting it too
	// would double-bill the loop.
	w.hists[perfSync].add(700 * time.Millisecond)
	assert.InDelta(t, 0.8, w.busyFraction(), 0.001)

	// A sliver window can be billed for work that began before it started.
	sliver := perfWindow{start: start, end: start.Add(time.Millisecond)}
	sliver.hists[perfEvent].add(50 * time.Millisecond)
	assert.Equal(t, 1.0, sliver.busyFraction(), "reported occupancy never exceeds 100%")
}

func TestDebugPerfCommand(t *testing.T) {
	m := newSnapshotModel(t)
	m, out, cmd, handled := m.applyLocalCommand("%debug perf")

	require.True(t, handled, "%debug perf is a client-side command")
	assert.Nil(t, cmd, "the report is built inline, with nothing to run")
	joined := strings.Join(out, "\n")
	assert.Contains(t, joined, "lifetime:")
	assert.Contains(t, joined, "trend (p95 latency per window")
	// The request itself belongs in the input-event ring like every other
	// command, so a snapshot taken afterwards shows it was asked for.
	assert.Contains(t, ringDescs(m.inputEvents), "perf report requested")
}

// ringDescs returns a ring's entry descriptions.
func ringDescs(r *ring) []string {
	var out []string
	for _, e := range r.entries() {
		out = append(out, e.desc)
	}
	return out
}

func TestDebugUsageListsPerf(t *testing.T) {
	m := newSnapshotModel(t)
	_, out, _, handled := m.applyLocalCommand("%debug")
	require.True(t, handled)
	assert.Contains(t, strings.Join(out, "\n"), "%debug perf")
}
