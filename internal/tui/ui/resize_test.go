package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newResizeModel builds a Model populated with a representative mix of output
// items. The client is never exercised by the resize path.
func newResizeModel(t *testing.T) Model {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.output = resizeFixture()
	return m
}

func resizeFixture() []OutputItem {
	longBody := "alpha bravo charlie delta echo foxtrot golf hotel india juliet " +
		"kilo lima mike november oscar papa quebec romeo sierra tango uniform " +
		"victor whiskey WATCHWORD xray yankee zulu one two three four five six " +
		"seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen " +
		"seventeen eighteen nineteen twenty twentyone twentytwo twentythree " +
		"twentyfour twentyfive twentysix twentyseven twentyeight twentynine thirty"
	emoteBody := " waves at everyone EMOTETOKEN in the room repeatedly and with " +
		"great enthusiasm spanning more than a single wrapped line for sure here"
	cmdLine := "CMDTOKEN this is a long single command output line that should be " +
		"wrapped by wordwrap when the terminal is narrow but not when it is wide"
	return []OutputItem{
		{Type: "text", Data: "hello world"},
		{Type: "event", Data: publicEvent("#1", "alice", "#5", "cafe", longBody)},
		{Type: "event", Data: emoteEvent("#3", "carol", "#5", "cafe", emoteBody)},
		{Type: "command", Data: []string{cmdLine}},
		{Type: "error", Data: "oops something ERRTOKEN happened"},
	}
}

func publicEvent(source, srcName, recip, recipName, body string) map[string]interface{} {
	return map[string]interface{}{
		"event":  "public",
		"source": source,
		"value":  body,
		"recips": []interface{}{recip},
		"entities": map[string]interface{}{
			source: map[string]interface{}{"name": srcName},
			recip:  map[string]interface{}{"name": recipName},
		},
	}
}

func emoteEvent(source, srcName, recip, recipName, body string) map[string]interface{} {
	return map[string]interface{}{
		"event":  "emote",
		"source": source,
		"value":  body,
		"recips": []interface{}{recip},
		"entities": map[string]interface{}{
			source: map[string]interface{}{"name": srcName},
			recip:  map[string]interface{}{"name": recipName},
		},
	}
}

func sizeTo(t *testing.T, m Model, w, h int) Model {
	t.Helper()
	upd, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return upd.(Model)
}

// renderedLines mirrors syncViewportContent's content assembly at the current width.
func renderedLines(m Model) []string {
	var lines []string
	for _, item := range m.output {
		lines = append(lines, m.renderOutputItem(item)...)
	}
	return lines
}

func TestResize_RewrapsWithinWidth(t *testing.T) {
	m := newResizeModel(t)
	for _, w := range []int{120, 80, 40, 200} {
		m = sizeTo(t, m, w, 24)
		for _, line := range renderedLines(m) {
			assert.LessOrEqualf(t, ansi.StringWidth(line), w,
				"line exceeds width %d: %q", w, ansi.Strip(line))
		}
	}
}

func TestResize_LongMessageWrapsMoreWhenNarrow(t *testing.T) {
	m := newResizeModel(t)
	// output[1] is the long public message.
	wide := sizeTo(t, m, 120, 24)
	narrow := sizeTo(t, m, 40, 24)
	wideLines := len(wide.renderOutputItem(wide.output[1]))
	narrowLines := len(narrow.renderOutputItem(narrow.output[1]))
	assert.Greater(t, narrowLines, wideLines,
		"narrow terminal should wrap the long message onto more lines")
}

func TestResize_NoMessagesLostOrTruncated(t *testing.T) {
	m := newResizeModel(t)
	// Distinctive tokens/words that must survive every rewrap (no word is split
	// because wrapping breaks on spaces).
	tokens := []string{"hello", "alice", "WATCHWORD", "carol", "EMOTETOKEN", "CMDTOKEN", "ERRTOKEN", "sixteen"}
	for _, w := range []int{120, 80, 40, 200} {
		m = sizeTo(t, m, w, 24)
		assert.Len(t, m.output, 5, "no output items dropped")
		joined := ansi.Strip(strings.Join(renderedLines(m), "\n"))
		for _, tok := range tokens {
			assert.Containsf(t, joined, tok, "token %q missing at width %d", tok, w)
		}
	}
}

func TestResize_AtBottomStaysAtBottom(t *testing.T) {
	m := newResizeModel(t)
	m = sizeTo(t, m, 120, 12)
	m.viewport.GotoBottom()
	require.True(t, m.viewport.AtBottom())

	for _, sz := range [][2]int{{80, 12}, {40, 20}, {200, 8}, {100, 30}} {
		m = sizeTo(t, m, sz[0], sz[1])
		assert.Truef(t, m.viewport.AtBottom(),
			"viewport should stay at bottom after resize to %dx%d", sz[0], sz[1])
	}
}

// makeBody returns a body of n space-separated words prefixed with token, so it
// wraps onto a width-dependent number of lines.
func makeBody(token string, n int) string {
	parts := make([]string, 0, n+1)
	parts = append(parts, token)
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf("w%03d", i))
	}
	return strings.Join(parts, " ")
}

// buildScrollModel returns a model whose anchor item (index 1) is preceded by a
// long message that rewraps with width and followed by a longer one (so the
// anchor can sit at the top without clamping). The pre-anchor rewrap is what
// makes a raw-offset restore land on the wrong message — i.e. it exercises the
// fix, not just the happy path.
func buildScrollModel(t *testing.T) (Model, int) {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.output = []OutputItem{
		{Type: "event", Data: publicEvent("#1", "alice", "#5", "cafe", makeBody("AAA", 80))},
		{Type: "text", Data: "ANCHORLINE"},
		{Type: "event", Data: publicEvent("#2", "bob", "#5", "cafe", makeBody("BBB", 240))},
	}
	return m, 1
}

func TestResize_WidthChangePreservesAnchor(t *testing.T) {
	m, anchor := buildScrollModel(t)
	m = sizeTo(t, m, 120, 8)

	// Scroll the anchor item to the top of the viewport.
	m.viewport.SetYOffset(m.itemStartLine(anchor))
	require.Equal(t, anchor, m.topVisibleItemIndex(), "precondition: anchor is at top before resize")

	// A pure width change rewraps the preceding message; without content anchoring
	// the raw offset would now land inside that message (jump). The anchor item
	// must remain at the top.
	m = sizeTo(t, m, 60, 8)
	assert.Equal(t, anchor, m.topVisibleItemIndex(),
		"width change should keep the anchored message at the top, not jump")
}

// TestStartup_RestoreAppliedInlineWhenSizeKnown locks in the fix for the
// intermittent "viewport jumps to the top on resize" bug. In the common startup
// ordering the initial WindowSizeMsg arrives before state loads. If the
// stored-position restore were deferred to "the next WindowSizeMsg", it would
// linger until the user's first manual resize and there override the resize
// anchor, yanking the viewport to a stale startup position. With the size
// already known, the restore must be consumed at state-load time so no later
// resize sees needsPositionRestore still set.
func TestStartup_RestoreAppliedInlineWhenSizeKnown(t *testing.T) {
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m = sizeTo(t, m, 80, 24) // initial WindowSizeMsg arrives first

	upd, _ := m.Update(initialStateMsg{state: &api.StateResponse{LastSeenID: 5}})
	m = upd.(Model)

	assert.False(t, m.needsPositionRestore,
		"restore must be applied inline when size is known, not deferred to a later resize")
}

// TestStartup_RestoreDeferredWhenSizeUnknown covers the racy ordering where
// state loads before any WindowSizeMsg. With no size yet, the restore must defer
// to the first WindowSizeMsg (which carries width 0 → no anchor, so no conflict).
func TestStartup_RestoreDeferredWhenSizeUnknown(t *testing.T) {
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)

	upd, _ := m.Update(initialStateMsg{state: &api.StateResponse{LastSeenID: 5}})
	m = upd.(Model)

	assert.True(t, m.needsPositionRestore,
		"restore must defer to the first WindowSizeMsg when size is unknown")
}

func TestResize_HeightOnlyPreservesTopItem(t *testing.T) {
	m, anchor := buildScrollModel(t)
	m = sizeTo(t, m, 120, 8)

	m.viewport.SetYOffset(m.itemStartLine(anchor))
	require.Equal(t, anchor, m.topVisibleItemIndex())

	// Height-only change (still small enough that content overflows): no rewrap,
	// top item preserved.
	m = sizeTo(t, m, 120, 11)
	assert.Equal(t, anchor, m.topVisibleItemIndex())
}
