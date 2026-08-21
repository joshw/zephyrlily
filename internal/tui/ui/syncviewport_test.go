package ui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncViewportContent appends only the items the view has not seen yet, which
// is what keeps a long session responsive — but it is only correct while its
// bookkeeping (syncedItems/syncedEpoch) agrees with the model. These tests
// compare the view's lines against a full re-render after every operation
// that could put the two out of step: plain appends, a width change, the
// debug split, and trimming at the scrollback cap.

// newSyncModel builds a sized model past the credential dialog, so key
// bindings reach the normal handler.
func newSyncModel(t *testing.T) Model {
	t.Helper()
	m := newDedupModel(t)
	m.authMode = false
	return m
}

// requireViewMatchesRebuild asserts the view holds exactly what rendering
// every item from scratch would produce.
func requireViewMatchesRebuild(t *testing.T, m Model, when string) {
	t.Helper()
	want := renderedLines(m)
	require.Equalf(t, len(want), m.viewport.TotalLineCount(), "%s: line count", when)
	require.Equalf(t, want, m.viewport.lines, "%s: line content", when)
	require.Equalf(t, len(m.output), m.syncedItems, "%s: synced item count", when)
	require.Equalf(t, m.renderEpoch, m.syncedEpoch, "%s: synced render epoch", when)
}

// deliverText sends n text messages through the normal proxy path.
func deliverText(t *testing.T, m Model, from int64, n int) Model {
	t.Helper()
	for i := 0; i < n; i++ {
		id := from + int64(i)
		msg := api.WSServerMsg{
			ID:   id,
			Type: "text",
			Data: map[string]interface{}{"text": fmt.Sprintf("message %d with some words in it", id)},
		}
		upd, _ := m.Update(serverEventMsg{msg: &msg})
		m = upd.(Model)
	}
	return m
}

func TestIncrementalSync_MatchesFullRebuild(t *testing.T) {
	m := newSyncModel(t)
	requireViewMatchesRebuild(t, m, "initial")

	m = deliverText(t, m, 1, 50)
	requireViewMatchesRebuild(t, m, "after 50 messages")

	// A width change rewraps every line: the epoch bump must force a rebuild
	// rather than leaving the old wrapping in place with new lines appended.
	m = sizeTo(t, m, 40, 24)
	requireViewMatchesRebuild(t, m, "after narrowing")

	m = deliverText(t, m, 51, 20)
	requireViewMatchesRebuild(t, m, "after messages at the new width")

	m = sizeTo(t, m, 100, 24)
	requireViewMatchesRebuild(t, m, "after widening")

	// A height-only change keeps every rendered line valid; the content must
	// survive it intact rather than being dropped or duplicated.
	m = sizeTo(t, m, 100, 40)
	requireViewMatchesRebuild(t, m, "after height-only resize")

	m = deliverText(t, m, 71, 10)
	requireViewMatchesRebuild(t, m, "after messages following resize")
}

func TestIncrementalSync_SurvivesDebugSplit(t *testing.T) {
	m := newSyncModel(t)
	m = deliverText(t, m, 1, 30)

	// The debug split halves the render width, which bumps the epoch.
	upd, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModAlt})
	m = upd.(Model)
	require.True(t, m.debugMode)
	requireViewMatchesRebuild(t, m, "debug split on")

	m = deliverText(t, m, 31, 10)
	requireViewMatchesRebuild(t, m, "messages while split")

	upd, _ = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModAlt})
	m = upd.(Model)
	require.False(t, m.debugMode)
	requireViewMatchesRebuild(t, m, "debug split off")
}

func TestIncrementalSync_TrimsAtScrollbackCap(t *testing.T) {
	m := newSyncModel(t)

	// Fill past the cap. The items go in directly and are synced in batches:
	// what is under test is the trim bookkeeping, and driving 10k messages
	// through Update one at a time would only make the test slow.
	for batch := 0; batch < 12; batch++ {
		for i := 0; i < 1000; i++ {
			m.output = append(m.output, OutputItem{
				Type: "text",
				Data: fmt.Sprintf("filler %d-%d", batch, i),
			})
		}
		m = m.syncViewportContent()
	}

	require.LessOrEqual(t, len(m.output), maxScrollback+scrollbackSlack, "scrollback stays capped")
	require.Greater(t, len(m.output), maxScrollback-scrollbackSlack, "trimming keeps the cap's worth")
	requireViewMatchesRebuild(t, m, "at the cap")

	// Messages arriving at the cap trim one item each: the view must keep
	// matching, having dropped the same lines off the top.
	m = deliverText(t, m, 1, 25)
	require.LessOrEqual(t, len(m.output), maxScrollback+scrollbackSlack)
	requireViewMatchesRebuild(t, m, "after messages at the cap")

	// The newest message must be visible in the view's tail, not lost to
	// an off-by-one in the trim.
	assert.Contains(t, m.viewport.lines[m.viewport.TotalLineCount()-1], "message 25")
}

// TestIncrementalSync_KeepsScrollPosition: appending below the fold must not
// move a reader who has scrolled up, which is the property that makes
// appending safe to do without re-anchoring.
func TestIncrementalSync_KeepsScrollPosition(t *testing.T) {
	m := newSyncModel(t)
	m = deliverText(t, m, 1, 100)

	m.viewport.SetYOffset(20)
	m.autoPageAnchor = -1
	offset := m.viewport.YOffset()

	m = deliverText(t, m, 101, 10)

	assert.Equal(t, offset, m.viewport.YOffset(), "new output must not scroll the reader")
	requireViewMatchesRebuild(t, m, "after appending below the fold")
}
