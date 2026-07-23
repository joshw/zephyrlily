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

// cmdYieldsClearScreen invokes cmd (which may be nil, a single Cmd, or a
// tea.Batch of several) and reports whether any resulting message is a
// tea.ClearScreen. Batched cmds surface as a tea.BatchMsg when invoked, so a
// single top-level type assertion isn't enough — this unwraps one level to
// check every command tea.Update built into the batch.
func cmdYieldsClearScreen(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	want := fmt.Sprintf("%T", tea.ClearScreen())
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if fmt.Sprintf("%T", c()) == want {
				return true
			}
		}
		return false
	}
	return fmt.Sprintf("%T", msg) == want
}

// TestResize_ShrinkingInputForcesRedraw is a regression test for a display
// corruption bug found by forensically replaying a real %debug snapshot: at
// the exact frame where the input area shrinks from 2 lines back to 1 (and
// the viewport grows to reclaim the row), the terminal could render content
// misplaced onto the wrong row — confirmed to be below this app's own
// renderInputArea(), which produces the correct string by hand-trace, and
// root-caused to a false-positive scroll-detection heuristic in mosh's
// Display::new_frame (mosh re-implements its own client-side terminal state,
// unlike ssh; plain ssh, GNU screen, iTerm2, and a from-scratch VT100
// emulator all render the same bytes correctly — see forceRedraw's doc
// comment in ui.go, and https://github.com/mobile-shell/mosh/issues/1400,
// for the full story). TestGrowBoundaryRendererByteStream proved the same
// boundary crossed by typing forward does NOT exhibit the bug, so the
// workaround only needs to fire on the shrink direction: maybeResizeViewport
// sets forceRedraw when the viewport grows, and Update wraps the cycle's Cmd
// with tea.ClearScreen to sidestep mosh's bug.
func TestResize_ShrinkingInputForcesRedraw(t *testing.T) {
	logChan, _ := NewLogger()
	base := New(client.New(""), logChan)
	base.authMode = false
	base = sizeTo(t, base, 20, 10) // firstWidth = 20 (no prompt)

	send := func(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
		upd, cmd := m.Update(msg)
		return upd.(Model), cmd
	}

	// 20 chars: n=len+1=21 > firstWidth=20, so two input lines.
	m := base
	m.inputValue = strings.Repeat("a", 20)
	m.inputCursor = 20
	m = m.maybeResizeViewport()
	require.Equal(t, 2, m.calculateInputHeight(), "fixture must start on two input lines")

	t.Run("backspace crossing 2->1 forces a redraw", func(t *testing.T) {
		got, cmd := send(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
		require.Equal(t, 19, len(got.inputValue))
		require.Equal(t, 1, got.calculateInputHeight(), "19 chars should fit on one input line")
		assert.False(t, got.forceRedraw, "flag must be consumed (cleared) by Update, not left set")
		assert.True(t, cmdYieldsClearScreen(t, cmd),
			"shrinking the input area must force a full repaint to avoid mosh's false-positive scroll-detection bug")
	})

	t.Run("backspace NOT crossing a line boundary does not force a redraw", func(t *testing.T) {
		m19 := m
		m19.inputValue = strings.Repeat("a", 19)
		m19.inputCursor = 19
		m19 = m19.maybeResizeViewport()
		m19.forceRedraw = false // fixture setup itself shrank a stale 2-line viewport; that's not what's under test
		require.Equal(t, 1, m19.calculateInputHeight())

		got, cmd := send(m19, tea.KeyPressMsg{Code: tea.KeyBackspace})
		require.Equal(t, 18, len(got.inputValue))
		require.Equal(t, 1, got.calculateInputHeight())
		assert.False(t, cmdYieldsClearScreen(t, cmd),
			"no line-count change means no need for the workaround")
	})

	t.Run("typing forward across 1->2 does not force a redraw", func(t *testing.T) {
		m19 := m
		m19.inputValue = strings.Repeat("a", 19)
		m19.inputCursor = 19
		m19 = m19.maybeResizeViewport()
		m19.forceRedraw = false // fixture setup itself shrank a stale 2-line viewport; that's not what's under test
		require.Equal(t, 1, m19.calculateInputHeight())

		got, cmd := send(m19, tea.KeyPressMsg{Code: 'a', Text: "a"})
		require.Equal(t, 20, len(got.inputValue))
		require.Equal(t, 2, got.calculateInputHeight(), "20 chars should need two input lines")
		assert.False(t, cmdYieldsClearScreen(t, cmd),
			"growth direction is confirmed clean (TestGrowBoundaryRendererByteStream); forcing a redraw here would just be extra flicker")
	})
}
