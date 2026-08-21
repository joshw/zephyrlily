package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// numbered returns n lines of distinguishable content.
func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

// newBubblesViewport builds the upstream viewport configured the way the
// output pane used to configure it, for parity comparisons.
func newBubblesViewport(width, height int, lines []string) viewport.Model {
	v := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	v.Style = lipgloss.NewStyle()
	v.SetContent(strings.Join(lines, "\n"))
	return v
}

// TestScrollViewMatchesBubblesViewport is the safety net for the swap: for
// every operation the app performs on the output pane, the replacement must
// agree with the viewport it replaced on both the scroll state and the exact
// rendered frame. Divergence here would surface as the -- MORE -- pager
// pausing in the wrong place, a resize landing on the wrong message, or a
// click hitting the wrong line.
func TestScrollViewMatchesBubblesViewport(t *testing.T) {
	const width, height = 40, 10

	// Content shorter than, exactly, and longer than the view - the boundaries
	// where offset clamping and height padding disagree if they disagree at all.
	for _, count := range []int{0, 1, 5, 10, 11, 200} {
		t.Run(fmt.Sprintf("lines=%d", count), func(t *testing.T) {
			lines := numbered(count)
			sv := newScrollView(width, height)
			sv.SetLines(append([]string(nil), lines...))
			vp := newBubblesViewport(width, height, lines)

			same := func(t *testing.T, what string) {
				t.Helper()
				assert.Equalf(t, vp.TotalLineCount(), sv.TotalLineCount(), "%s: total lines", what)
				assert.Equalf(t, vp.YOffset(), sv.YOffset(), "%s: offset", what)
				assert.Equalf(t, vp.AtBottom(), sv.AtBottom(), "%s: at bottom", what)
				assert.Equalf(t, vp.View(), sv.View(), "%s: rendered frame", what)
			}
			same(t, "after set")

			vp.GotoBottom()
			sv.GotoBottom()
			same(t, "goto bottom")

			vp.PageUp()
			sv.PageUp()
			same(t, "page up")

			vp.ScrollDown(3)
			sv.ScrollDown(3)
			same(t, "scroll down 3")

			vp.ScrollUp(1)
			sv.ScrollUp(1)
			same(t, "scroll up 1")

			// Offsets past either end must clamp identically.
			vp.SetYOffset(-5)
			sv.SetYOffset(-5)
			same(t, "offset below zero")

			vp.SetYOffset(count + 100)
			sv.SetYOffset(count + 100)
			same(t, "offset past end")

			vp.GotoTop()
			sv.GotoTop()
			same(t, "goto top")
		})
	}
}

// TestScrollViewEmptyContentMatchesBubbles pins the one case where a naive
// implementation diverges: a single empty line is no content at all, and
// TotalLineCount is what the auto-pager anchors on.
func TestScrollViewEmptyContentMatchesBubbles(t *testing.T) {
	sv := newScrollView(20, 5)
	sv.SetContent("")
	vp := newBubblesViewport(20, 5, []string{""})

	assert.Equal(t, vp.TotalLineCount(), sv.TotalLineCount())
	assert.Equal(t, 0, sv.TotalLineCount())
	assert.Equal(t, vp.View(), sv.View())
}

// TestScrollViewAppendEqualsSetAll is the invariant the incremental sync rests
// on: appending in pieces must leave exactly the state a single set would.
func TestScrollViewAppendEqualsSetAll(t *testing.T) {
	all := numbered(100)

	incremental := newScrollView(30, 8)
	for i := 0; i < len(all); i += 7 {
		incremental.AppendLines(append([]string(nil), all[i:min(i+7, len(all))]...))
	}
	whole := newScrollView(30, 8)
	whole.SetLines(append([]string(nil), all...))

	assert.Equal(t, whole.TotalLineCount(), incremental.TotalLineCount())
	incremental.GotoBottom()
	whole.GotoBottom()
	assert.Equal(t, whole.View(), incremental.View())
}

// TestScrollViewAppendKeepsPosition: lines arriving below the fold must not
// move what the reader is looking at. This is why appending is safe to do
// without touching the offset.
func TestScrollViewAppendKeepsPosition(t *testing.T) {
	sv := newScrollView(30, 5)
	sv.SetLines(numbered(50))
	sv.SetYOffset(10)
	before := sv.View()

	sv.AppendLines(numbered(20))

	assert.Equal(t, 10, sv.YOffset())
	assert.Equal(t, before, sv.View(), "content below the fold must not disturb the view")
	assert.Equal(t, 70, sv.TotalLineCount())
}

// TestScrollViewTrimTopKeepsView: dropping lines off the top is the scrollback
// cap doing its work, and the reader must stay on the same content rather than
// having it slide upward under them.
func TestScrollViewTrimTopKeepsView(t *testing.T) {
	sv := newScrollView(30, 5)
	sv.SetLines(numbered(100))
	sv.SetYOffset(40)
	before := sv.View()

	sv.TrimTop(10)

	assert.Equal(t, 30, sv.YOffset(), "the offset follows the content up")
	assert.Equal(t, before, sv.View())
	assert.Equal(t, 90, sv.TotalLineCount())
}

func TestScrollViewTrimTopEdgeCases(t *testing.T) {
	sv := newScrollView(30, 5)
	sv.SetLines(numbered(10))
	sv.SetYOffset(3)

	sv.TrimTop(0)
	assert.Equal(t, 10, sv.TotalLineCount(), "trimming nothing changes nothing")
	assert.Equal(t, 3, sv.YOffset())

	// Trimming at or past the end empties the view rather than leaving a
	// stale offset pointing into content that is gone.
	sv.TrimTop(50)
	assert.Equal(t, 0, sv.TotalLineCount())
	assert.Equal(t, 0, sv.YOffset())
	assert.True(t, sv.AtBottom())
}

// TestScrollViewTrimTopStaysBounded: the cap trims one item at a time for the
// rest of a session's life, so re-slicing must not accumulate dead lines in
// the backing array without bound.
func TestScrollViewTrimTopStaysBounded(t *testing.T) {
	sv := newScrollView(30, 5)
	sv.SetLines(numbered(1000))
	for i := 0; i < 20000; i++ {
		sv.AppendLines([]string{"new"})
		sv.TrimTop(1)
	}
	require.Equal(t, 1000, sv.TotalLineCount())
	assert.LessOrEqual(t, cap(sv.lines), 8*sv.TotalLineCount(),
		"re-slicing must let the backing array be recycled, not grow with the session")
}

func TestScrollViewSetHeightKeepsOffsetInRange(t *testing.T) {
	sv := newScrollView(30, 5)
	sv.SetLines(numbered(20))
	sv.GotoBottom()
	require.Equal(t, 15, sv.YOffset())

	// A taller view would otherwise sit past the end of its own content.
	sv.SetHeight(20)
	assert.Equal(t, 0, sv.YOffset())
	assert.True(t, sv.AtBottom())
}

func TestScrollViewZeroSizeRendersNothing(t *testing.T) {
	sv := newScrollView(0, 0)
	sv.SetLines(numbered(10))
	assert.Equal(t, "", sv.View())
}
