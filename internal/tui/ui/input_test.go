package ui

import (
	"fmt"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasteMode(t *testing.T) {
	tests := []struct {
		name          string
		pasteText     string
		initialValue  string
		expectedValue string
		description   string
	}{
		{
			name:          "single_newline_lf",
			pasteText:     "c\nd",
			initialValue:  "",
			expectedValue: "c d",
			description:   "Paste with LF newline should convert to space",
		},
		{
			name:          "single_newline_cr",
			pasteText:     "c\rd",
			initialValue:  "",
			expectedValue: "c d",
			description:   "Paste with CR newline should convert to space",
		},
		{
			name:          "consecutive_newlines",
			pasteText:     "a\r\rb",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Consecutive newlines should be eaten to single space",
		},
		{
			name:          "consecutive_spaces",
			pasteText:     "a  b",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Consecutive spaces should be eaten to single space",
		},
		{
			name:          "mixed_whitespace",
			pasteText:     "a \r b",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Mixed space and newline should normalize",
		},
		{
			name:          "space_then_newline",
			pasteText:     "a \rb",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Space then newline should be eaten",
		},
		{
			name:          "newline_then_space",
			pasteText:     "a\r b",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Newline then space should be eaten",
		},
		{
			name:          "leading_whitespace",
			pasteText:     "\r\n  hello",
			initialValue:  "",
			expectedValue: " hello",
			description:   "Leading whitespace should normalize to single space",
		},
		{
			name:          "trailing_whitespace",
			pasteText:     "hello\r\n  ",
			initialValue:  "",
			expectedValue: "hello ",
			description:   "Trailing whitespace should normalize",
		},
		{
			name:          "multiline_with_text",
			pasteText:     "line1\rline2\nline3",
			initialValue:  "",
			expectedValue: "line1 line2 line3",
			description:   "Multi-line text should convert newlines to spaces",
		},
		{
			name:          "append_to_existing",
			pasteText:     "b\rc",
			initialValue:  "a ",
			expectedValue: "a b c",
			description:   "Pasting onto existing text should work",
		},
		{
			name:          "tab_between_words",
			pasteText:     "a\tb",
			initialValue:  "",
			expectedValue: "a b",
			description:   "Tab should collapse to a single space like other whitespace",
		},
		{
			name:          "url_then_tab_then_text",
			pasteText:     "url \tI'm",
			initialValue:  "",
			expectedValue: "url I'm",
			description:   "Space followed by tab should collapse to a single space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				pasteMode:    true,
				inputValue:   tt.initialValue,
				inputCursor:  len(tt.initialValue),
				pasteEatFlag: false,
				pasteEatBuf:  false,
			}

			// Feed the pasted text through the real paste-normalization path.
			for _, r := range tt.pasteText {
				m = m.pasteRune(r)
			}

			if m.inputValue != tt.expectedValue {
				t.Errorf("%s: %s\n  got:      %q\n  expected: %q", tt.name, tt.description, m.inputValue, tt.expectedValue)
			}
		})
	}
}

// TestSubmitLineAnchorRespectsScrollPosition verifies that submitting a line
// only arms the auto-page anchor (which scrolls the viewport down to the
// response) when the user was already following the bottom. Scrolled back into
// history, a submit must leave the viewport where it is.
func TestSubmitLineAnchorRespectsScrollPosition(t *testing.T) {
	build := func() Model {
		logChan, _ := NewLogger()
		m := New(client.New(""), logChan)
		for i := 0; i < 40; i++ {
			m.output = append(m.output, OutputItem{Type: "text", Data: fmt.Sprintf("line %02d", i)})
		}
		m = sizeTo(t, m, 80, 6)
		require.Greater(t, m.viewport.TotalLineCount(), m.viewport.Height(),
			"fixture must be tall enough to scroll")
		return m
	}

	t.Run("scrolled back keeps anchor disabled and viewport put", func(t *testing.T) {
		m := build()
		m.viewport.GotoTop()
		require.False(t, m.viewport.AtBottom())
		before := m.viewport.YOffset()

		// %page exercises submitLine's anchor logic (set unconditionally at the
		// top) without reaching client.Send, which the test client can't service.
		m, _ = m.submitLine("%page")

		assert.Equal(t, -1, m.autoPageAnchor, "anchor must stay disabled when scrolled back")
		assert.Equal(t, before, m.viewport.YOffset(), "viewport must not jump when scrolled back")
	})

	t.Run("at bottom arms anchor and follows the response", func(t *testing.T) {
		m := build()
		m.viewport.GotoBottom()
		require.True(t, m.viewport.AtBottom())

		m, _ = m.submitLine("%page")

		assert.GreaterOrEqual(t, m.autoPageAnchor, 0, "anchor must be armed when caught up at bottom")
		assert.True(t, m.viewport.AtBottom(), "viewport should keep following the bottom")
	})
}

// TestPagerArmsOnCatchUp verifies that reaching the bottom by any user
// interaction — not just submitting a line — re-arms the auto-page anchor, so
// output that trickles in one message at a time while the user is idle pauses
// at -- MORE -- instead of following the bottom indefinitely. Regression test
// for "came back after 12 hours and nothing was paged up": previously only
// submitLine armed the anchor, and every scroll key / blank-Enter advance
// disarmed it for good.
func TestPagerArmsOnCatchUp(t *testing.T) {
	build := func() Model {
		logChan, _ := NewLogger()
		m := New(client.New(""), logChan)
		for i := 0; i < 40; i++ {
			m.output = append(m.output, OutputItem{Type: "text", Data: fmt.Sprintf("line %02d", i)})
		}
		m = sizeTo(t, m, 80, 6)
		require.Greater(t, m.viewport.TotalLineCount(), m.viewport.Height(),
			"fixture must be tall enough to scroll")
		return m
	}

	// trickle appends single-line items one at a time, syncing after each, the
	// way live server events arrive.
	trickle := func(m Model, n int) Model {
		for i := 0; i < n; i++ {
			m.output = append(m.output, OutputItem{Type: "text", Data: fmt.Sprintf("new %02d", i)})
			m = m.syncViewportContent()
		}
		return m
	}

	t.Run("goto-bottom key re-arms and trickled output pauses", func(t *testing.T) {
		m := build()
		m.viewport.GotoTop()
		m.autoPageAnchor = -1

		// M-> (goto bottom) catches up to the newest output.
		upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: '>', Mod: tea.ModAlt})
		m = upd.(Model)
		require.True(t, m.viewport.AtBottom())
		require.GreaterOrEqual(t, m.autoPageAnchor, 0, "catching up must re-arm the anchor")
		anchor := m.autoPageAnchor

		// Under a page of new output: keep following the bottom, anchor stays armed.
		m = trickle(m, m.viewport.Height()-1)
		assert.True(t, m.viewport.AtBottom(), "should follow the bottom while under a page")
		assert.Equal(t, anchor, m.autoPageAnchor, "anchor must stay armed while following")

		// Crossing a page: pause showing one page past the anchor.
		m = trickle(m, 2)
		assert.False(t, m.viewport.AtBottom(), "must pause once a page accumulates")
		assert.Equal(t, anchor, m.viewport.YOffset(), "pause must show one page from the anchor")
		assert.Equal(t, -1, m.autoPageAnchor, "anchor disarms after firing")

		// Still idle: further output must not move the paused view.
		m = trickle(m, 10)
		assert.Equal(t, anchor, m.viewport.YOffset(), "paused view must hold while output accumulates")
	})

	t.Run("blank-Enter pager advance to the bottom re-arms", func(t *testing.T) {
		m := build()
		m.viewport.GotoTop()
		m.autoPageAnchor = -1

		for i := 0; !m.viewport.AtBottom(); i++ {
			require.Less(t, i, 100, "pager advance must terminate")
			upd, _ := m.handleSubmit() // empty input while behind: advances the pager
			m = upd.(Model)
		}
		assert.GreaterOrEqual(t, m.autoPageAnchor, 0, "reaching bottom via blank Enter must re-arm")
	})

	t.Run("scrolling away from the bottom leaves the anchor disarmed", func(t *testing.T) {
		m := build()
		m.viewport.GotoBottom()

		upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
		m = upd.(Model)
		require.False(t, m.viewport.AtBottom())
		assert.Equal(t, -1, m.autoPageAnchor, "scrolled back must stay disarmed")
	})
}

func TestPasteModeWithEnterKey(t *testing.T) {
	tests := []struct {
		name          string
		actions       []string // "text" or "enter"
		expectedValue string
	}{
		{
			name:          "text_enter_text",
			actions:       []string{"a", "enter", "b"},
			expectedValue: "a b",
		},
		{
			name:          "consecutive_enters",
			actions:       []string{"a", "enter", "enter", "b"},
			expectedValue: "a b",
		},
		{
			name:          "space_then_enter",
			actions:       []string{"a", " ", "enter", "b"},
			expectedValue: "a b",
		},
		{
			name:          "enter_then_space",
			actions:       []string{"a", "enter", " ", "b"},
			expectedValue: "a b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{
				pasteMode:    true,
				inputValue:   "",
				inputCursor:  0,
				pasteEatFlag: false,
				pasteEatBuf:  false,
			}

			for _, action := range tt.actions {
				if action == "enter" {
					// Enter is treated as a newline rune by the paste path.
					m = m.pasteRune('\n')
				} else {
					for _, r := range action {
						m = m.pasteRune(r)
					}
				}
			}

			if m.inputValue != tt.expectedValue {
				t.Errorf("%s: got %q, expected %q", tt.name, m.inputValue, tt.expectedValue)
			}
		})
	}
}

// TestSearchKeysIgnoredInPasteMode: C-r/C-s must not start incremental search
// while paste mode is active. handleSearchKey bypasses paste handling, so a
// search begun there left the user unable to move the cursor or toggle paste
// mode off until the search was dismissed with ESC.
func TestSearchKeysIgnoredInPasteMode(t *testing.T) {
	newModel := func(paste bool) Model {
		return Model{
			keys:        NewKeyMap(),
			input:       textarea.New(),
			width:       80,
			height:      24,
			pasteMode:   paste,
			inputValue:  "abc;def?qrs",
			inputCursor: len("abc;def?qrs"),
		}
	}

	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"ctrl+r", tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}},
		{"ctrl+s", tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}},
	} {
		t.Run(tc.name+"_paste_mode", func(t *testing.T) {
			upd, _ := newModel(true).handleNormalKey(tc.msg)
			m := upd.(Model)
			if m.searchMode {
				t.Fatalf("%s in paste mode must not enter search mode", tc.name)
			}
			if m.inputValue != "abc;def?qrs" {
				t.Fatalf("%s in paste mode changed input to %q", tc.name, m.inputValue)
			}
		})
		t.Run(tc.name+"_normal_mode", func(t *testing.T) {
			upd, _ := newModel(false).handleNormalKey(tc.msg)
			if !upd.(Model).searchMode {
				t.Fatalf("%s outside paste mode should enter search mode", tc.name)
			}
		})
	}
}

// TestWordMotionWorksInPasteMode: M-f/M-b arrive as KeyRunes with the Alt flag
// set, and the paste-mode rune interception must not swallow them as pasted
// text — they should move the cursor by a word like they do in normal mode.
func TestWordMotionWorksInPasteMode(t *testing.T) {
	newModel := func(cursor int) Model {
		return Model{
			keys:        NewKeyMap(),
			input:       textarea.New(),
			width:       80,
			height:      24,
			pasteMode:   true,
			inputValue:  "foo bar",
			inputCursor: cursor,
		}
	}

	t.Run("alt+b_moves_back_a_word", func(t *testing.T) {
		upd, _ := newModel(len("foo bar")).handleNormalKey(
			tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt})
		m := upd.(Model)
		if want := len("foo "); m.inputCursor != want {
			t.Fatalf("M-b in paste mode: cursor = %d, want %d", m.inputCursor, want)
		}
		if m.inputValue != "foo bar" {
			t.Fatalf("M-b in paste mode changed input to %q", m.inputValue)
		}
	})

	t.Run("alt+f_moves_forward_a_word", func(t *testing.T) {
		upd, _ := newModel(0).handleNormalKey(
			tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt})
		m := upd.(Model)
		if want := len("foo"); m.inputCursor != want {
			t.Fatalf("M-f in paste mode: cursor = %d, want %d", m.inputCursor, want)
		}
		if m.inputValue != "foo bar" {
			t.Fatalf("M-f in paste mode changed input to %q", m.inputValue)
		}
	})

	// The ESC-prefix spelling (ESC then 'b' == M-b) must take the same path.
	t.Run("esc_then_b_moves_back_a_word", func(t *testing.T) {
		upd, _ := newModel(len("foo bar")).handleNormalKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		upd, _ = upd.(Model).handleNormalKey(tea.KeyPressMsg{Code: 'b', Text: "b"})
		m := upd.(Model)
		if want := len("foo "); m.inputCursor != want {
			t.Fatalf("ESC b in paste mode: cursor = %d, want %d", m.inputCursor, want)
		}
		if m.inputValue != "foo bar" {
			t.Fatalf("ESC b in paste mode changed input to %q", m.inputValue)
		}
	})
}

// TestMetaPrefixDoesNotSwallowNonRuneKeys reproduces the bug where a stray ESC
// (left over from a split Alt-word-motion escape sequence) primed metaPrefix
// and caused the *next* Ctrl-B / Ctrl-F / arrow key to be alt-ified into an
// unbound combo and silently swallowed. Non-rune keys have no M- bindings, so a
// pending metaPrefix must not consume them.
func TestMetaPrefixDoesNotSwallowNonRuneKeys(t *testing.T) {
	send := func(m Model, msg tea.KeyPressMsg) Model {
		next, _ := m.handleNormalKey(msg)
		return next.(Model)
	}

	newModel := func() Model {
		return Model{
			keys:        NewKeyMap(),
			input:       textarea.New(),
			width:       80,
			height:      24,
			inputValue:  "foo, bar",
			inputCursor: len("foo, bar"),
		}
	}

	nonRune := []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"ctrl+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}},
		{"ctrl+f", tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}},
		{"left", tea.KeyPressMsg{Code: tea.KeyLeft}},
		{"right", tea.KeyPressMsg{Code: tea.KeyRight}},
	}

	for _, tc := range nonRune {
		t.Run("prefix_then_"+tc.name, func(t *testing.T) {
			m := newModel()
			m.inputCursor = 4 // mid-line so both back and forward motions can move
			// A stray ESC primes the meta prefix.
			m = send(m, tea.KeyPressMsg{Code: tea.KeyEscape})
			before := m.inputCursor
			m = send(m, tc.msg)
			if m.inputCursor == before {
				t.Fatalf("%s after meta prefix was swallowed: cursor stayed at %d", tc.name, before)
			}
			if m.metaPrefix {
				t.Fatalf("metaPrefix should be cleared after %s", tc.name)
			}
		})
	}

	// Genuine ESC-as-Meta on a rune still works: ESC then 'b' is M-b (word back).
	t.Run("prefix_then_rune_b_is_word_back", func(t *testing.T) {
		m := newModel() // cursor at end of "foo, bar"
		m = send(m, tea.KeyPressMsg{Code: tea.KeyEscape})
		m = send(m, tea.KeyPressMsg{Code: 'b', Text: "b"})
		if want := len("foo, "); m.inputCursor != want {
			t.Fatalf("M-b should move to start of word: got cursor %d, want %d", m.inputCursor, want)
		}
	})
}

func TestDoubleCtrlCToQuit(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

	newModel := func(t *testing.T) Model {
		logChan, _ := NewLogger()
		m := New(client.New(""), logChan)
		m.authMode = false
		return sizeTo(t, m, 80, 24)
	}

	t.Run("first C-c warns instead of quitting", func(t *testing.T) {
		m := newModel(t)
		upd, cmd := m.handleNormalKey(ctrlC)
		m = upd.(Model)
		assert.Nil(t, cmd, "first C-c must not quit")
		assert.True(t, m.quitPending)
		require.NotEmpty(t, m.output)
		assert.Equal(t, "Press C-c again to exit.", m.output[len(m.output)-1].Data)
	})

	t.Run("second consecutive C-c quits", func(t *testing.T) {
		m := newModel(t)
		upd, _ := m.handleNormalKey(ctrlC)
		_, cmd := upd.(Model).handleNormalKey(ctrlC)
		require.NotNil(t, cmd)
		assert.Equal(t, tea.Quit(), cmd(), "second C-c must quit")
	})

	t.Run("any other key cancels the pending quit", func(t *testing.T) {
		m := newModel(t)
		upd, _ := m.handleNormalKey(ctrlC)
		upd, _ = upd.(Model).handleNormalKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
		m = upd.(Model)
		assert.False(t, m.quitPending, "typing must cancel the pending quit")
		upd, cmd := m.handleNormalKey(ctrlC)
		assert.Nil(t, cmd, "C-c after a cancel must warn again, not quit")
		assert.True(t, upd.(Model).quitPending)
	})

	t.Run("auth dialog also requires double C-c", func(t *testing.T) {
		logChan, _ := NewLogger()
		m := New(client.New(""), logChan)
		m.authMode = true
		upd, cmd := m.handleAuthKey(ctrlC)
		m = upd.(Model)
		assert.Nil(t, cmd, "first C-c in auth dialog must not quit")
		assert.True(t, m.quitPending)
		upd, cmd = m.handleAuthKey(ctrlC)
		require.NotNil(t, cmd)
		assert.Equal(t, tea.Quit(), cmd(), "second C-c in auth dialog must quit")
		_ = upd
	})
}
