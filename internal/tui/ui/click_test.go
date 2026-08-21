package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newClickModel builds a sized, non-auth Model with mouse reporting on and the
// given text already in the input line.
func newClickModel(t *testing.T, w, h int, prompt, value string) Model {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false
	m.mouseEnabled = true
	m.prompt = prompt
	m = sizeTo(t, m, w, h)
	m.inputValue = value
	m.inputCursor = 0
	m.syncTextarea()
	m = m.maybeResizeViewport()
	return m
}

// inputCells returns the input area as ANSI-stripped rows of runes, indexed the
// way the terminal addresses them: cells[line][column].
func inputCells(m Model) [][]rune {
	var cells [][]rune
	for _, line := range strings.Split(m.renderInputArea(), "\n") {
		cells = append(cells, []rune(ansi.Strip(line)))
	}
	return cells
}

// TestInputHitTest_LandsOnTheClickedCharacter is the core contract: for every
// cell the input area draws a character into, hit-testing that cell returns the
// offset of exactly that character. It walks the rendered frame rather than
// re-deriving the layout, so it would catch the hit test and the renderer
// drifting apart.
func TestInputHitTest_LandsOnTheClickedCharacter(t *testing.T) {
	cases := []struct {
		name   string
		width  int
		prompt string
		value  string
	}{
		{"single line, no prompt", 40, "", "hello world"},
		{"single line, prompt", 40, "--> ", "hello world"},
		{"wrapped, no prompt", 20, "", strings.Repeat("abcdefghij", 5)},
		{"wrapped, prompt", 20, "--> ", strings.Repeat("abcdefghij", 5)},
		{"wrapped mid-word, prompt", 13, "-> ", "the quick brown fox jumps over it"},
		{"exactly fills first line", 20, "--> ", "0123456789012345"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newClickModel(t, tc.width, 24, tc.prompt, tc.value)
			cells := inputCells(m)
			top := m.inputTopRow()

			checked := 0
			for lineIdx, row := range cells {
				start := m.inputLineStart(lineIdx)
				end := m.inputLineEnd(lineIdx)
				// Column where this line's first input byte is drawn.
				base := 0
				if lineIdx == 0 {
					base = m.inputPromptRenderWidth()
				}
				for off := start; off < end; off++ {
					col := base + (off - start)
					require.Less(t, col, len(row),
						"line %d column %d is past the rendered row %q",
						lineIdx, col, string(row))

					got, ok := m.inputHitTest(col, top+lineIdx)
					require.True(t, ok, "cell (%d,%d) should be inside the input area", col, top+lineIdx)
					assert.Equalf(t, off, got,
						"click on line %d column %d (drawn char %q) should select offset %d, the byte %q",
						lineIdx, col, string(row[col]), off, string(tc.value[off]))
					assert.Equalf(t, rune(tc.value[off]), row[col],
						"renderer drew %q at line %d column %d, expected %q",
						string(row[col]), lineIdx, col, string(tc.value[off]))
					checked++
				}
			}
			assert.Equal(t, len(tc.value), checked,
				"every byte of the input should be reachable by a click")
		})
	}
}

func TestInputHitTest_OutsideInputArea(t *testing.T) {
	m := newClickModel(t, 40, 24, "--> ", "hello")
	top := m.inputTopRow()

	_, ok := m.inputHitTest(5, top-1)
	assert.False(t, ok, "the status bar row is not the input area")
	_, ok = m.inputHitTest(5, 0)
	assert.False(t, ok, "the viewport is not the input area")
	_, ok = m.inputHitTest(5, top+m.calculateInputHeight())
	assert.False(t, ok, "below the last input line is not the input area")
}

func TestInputHitTest_ClampsPastEndOfText(t *testing.T) {
	m := newClickModel(t, 40, 24, "--> ", "hello")
	top := m.inputTopRow()

	off, ok := m.inputHitTest(35, top)
	require.True(t, ok)
	assert.Equal(t, len("hello"), off, "clicking past the text should go to the end of the line")
}

func TestInputHitTest_ClickOnPromptGoesToLineStart(t *testing.T) {
	m := newClickModel(t, 40, 24, "--> ", "hello")
	top := m.inputTopRow()

	off, ok := m.inputHitTest(1, top)
	require.True(t, ok, "the prompt is still inside the input area")
	assert.Equal(t, 0, off, "clicking the prompt should put the cursor at the start of the line")
}

// click drives a left button press through Update, the way the terminal does.
func click(t *testing.T, m Model, x, y int) Model {
	t.Helper()
	upd, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
	return upd.(Model)
}

func TestClick_MovesCursorThroughUpdate(t *testing.T) {
	m := newClickModel(t, 40, 24, "--> ", "hello world")
	m.inputCursor = len(m.inputValue)

	m = click(t, m, m.inputPromptRenderWidth()+6, m.inputTopRow())
	assert.Equal(t, 6, m.inputCursor, "click should move the cursor onto the 'w' of world")
	assert.Equal(t, 6, m.input.LineInfo().ColumnOffset,
		"handleInputClick should sync the display textarea to the new position")
}

func TestClick_IgnoredWhenMouseReportingOff(t *testing.T) {
	m := newClickModel(t, 40, 24, "--> ", "hello world")
	m.mouseEnabled = false
	m.inputCursor = 11

	m = click(t, m, m.inputPromptRenderWidth()+6, m.inputTopRow())
	assert.Equal(t, 11, m.inputCursor,
		"without %mouse on the terminal reports no clicks; state must not change")
}

func TestClick_IgnoredInAuthAndEditModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Model)
	}{
		{"auth", func(m *Model) { m.authMode = true }},
		{"edit", func(m *Model) { m.editMode = true }},
		{"reconnect", func(m *Model) { m.reconnectPrompt = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newClickModel(t, 40, 24, "--> ", "hello world")
			m.inputCursor = 11
			tc.set(&m)
			m = click(t, m, m.inputPromptRenderWidth()+6, m.inputTopRow())
			assert.Equal(t, 11, m.inputCursor, "%s mode owns the screen; clicks must not reposition", tc.name)
		})
	}
}

func TestClick_NonLeftButtonIgnored(t *testing.T) {
	m := newClickModel(t, 40, 24, "--> ", "hello world")
	m.inputCursor = 11

	upd, _ := m.Update(tea.MouseClickMsg{Button: tea.MouseRight, X: m.inputPromptRenderWidth() + 6, Y: m.inputTopRow()})
	assert.Equal(t, 11, upd.(Model).inputCursor, "only the left button repositions the cursor")
}

func TestClick_OnSecondLineOfWrappedInput(t *testing.T) {
	value := strings.Repeat("abcdefghij", 4)
	m := newClickModel(t, 20, 24, "--> ", value)
	require.Greater(t, m.calculateInputHeight(), 1, "input should wrap onto multiple lines")

	m = click(t, m, 3, m.inputTopRow()+1)
	assert.Equal(t, m.inputLineStart(1)+3, m.inputCursor,
		"a click on a continuation line should offset from that line's start")
}

func TestClick_EndsIncrementalSearch(t *testing.T) {
	m := newClickModel(t, 40, 24, "", "")
	m.history = []string{"hello world"}
	m = m.enterSearch(true)
	m.searchBuf = "hello"
	m = m.searchRefresh()
	m = m.maybeResizeViewport()
	require.True(t, m.searchMode)
	require.Equal(t, "hello world", m.inputValue)

	m = click(t, m, m.inputPromptRenderWidth()+6, m.inputTopRow())
	assert.False(t, m.searchMode, "clicking into the line should accept the match and leave search")
	assert.Equal(t, 6, m.inputCursor)
}

func TestClick_DismissesCompletionPopup(t *testing.T) {
	m := newClickModel(t, 40, 24, "--> ", "hello world")
	m.completionActive = true

	m = click(t, m, m.inputPromptRenderWidth()+6, m.inputTopRow())
	assert.False(t, m.completionActive, "a click should dismiss the completion popup")
	assert.Equal(t, 6, m.inputCursor)
}

// The kill ring appends only across consecutive kills; a click is a cursor move
// and must break that run, exactly as C-a or an arrow key does.
func TestClick_BreaksKillAppend(t *testing.T) {
	m := newClickModel(t, 40, 24, "--> ", "hello world")
	m.lastKill = true

	m = click(t, m, m.inputPromptRenderWidth()+6, m.inputTopRow())
	assert.False(t, m.lastKill, "a click should end a kill sequence")
}
