package ui

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// typeSearch simulates entering incremental search (backward or forward) and
// typing the pattern one rune at a time via searchRefresh, the same path the
// real key handler takes.
func typeSearch(m Model, backward bool, pattern string) Model {
	m = m.enterSearch(backward)
	for _, r := range pattern {
		m.searchBuf += string(r)
		m = m.searchRefresh()
	}
	return m
}

// TestSearchMatchSpan: the span reported for in-place highlighting must cover
// the current match, and must report no span for an empty pattern or a failing
// search (pattern absent from the line at the recorded position).
func TestSearchMatchSpan(t *testing.T) {
	line := "this is a test and also a test"
	m := Model{inputValue: line, inputCursor: len(line)}
	m = typeSearch(m, true, "TEST") // uppercase: matching is case-insensitive

	start, end, ok := m.searchMatchSpan()
	wantStart := len("this is a test and also a ")
	if !ok || start != wantStart || end != wantStart+len("test") {
		t.Fatalf("searchMatchSpan = (%d, %d, %v), want (%d, %d, true)",
			start, end, ok, wantStart, wantStart+len("test"))
	}

	// Extending the pattern so nothing matches leaves the previous line and
	// position in place; the span must vanish rather than highlight stale text.
	m.searchBuf += "zzz"
	m = m.searchRefresh()
	if _, _, ok := m.searchMatchSpan(); ok {
		t.Fatalf("failing search still reported a match span")
	}

	// Empty pattern: nothing to highlight.
	m2 := Model{inputValue: line, inputCursor: len(line)}.enterSearch(true)
	if _, _, ok := m2.searchMatchSpan(); ok {
		t.Fatalf("empty pattern reported a match span")
	}

	// Outside search mode: never a span.
	m.searchMode = false
	if _, _, ok := m.searchMatchSpan(); ok {
		t.Fatalf("searchMatchSpan reported a span outside search mode")
	}
}

// TestCursorMotionEndsSearch: any cursor-movement key during incremental
// search must end the search, keep the matched line, and apply the motion from
// the cursor position at the start of the match.
func TestCursorMotionEndsSearch(t *testing.T) {
	line := "this is a test and also a test"
	matchStart := len("this is a test and also a ")
	newModel := func() Model {
		m := Model{
			keys:        NewKeyMap(),
			input:       textarea.New(),
			width:       80,
			height:      24,
			inputValue:  line,
			inputCursor: len(line),
		}
		return typeSearch(m, true, "test")
	}

	// The expected cursor for each motion is whatever that key does in normal
	// mode starting from the match-start position — the spec verbatim.
	wantCursorFor := func(msg tea.KeyPressMsg) int {
		ref := Model{
			keys:        NewKeyMap(),
			input:       textarea.New(),
			width:       80,
			height:      24,
			inputValue:  line,
			inputCursor: matchStart,
		}
		upd, _ := ref.handleNormalKey(msg)
		return upd.(Model).inputCursor
	}

	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"ctrl+f", tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}},
		{"right", tea.KeyPressMsg{Code: tea.KeyRight}},
		{"ctrl+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}},
		{"left", tea.KeyPressMsg{Code: tea.KeyLeft}},
		{"ctrl+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}},
		{"ctrl+e", tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}},
		{"alt+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt}},
		{"alt+f", tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel()
			if m.inputCursor != matchStart {
				t.Fatalf("fixture: cursor = %d, want %d (match start)", m.inputCursor, matchStart)
			}
			upd, _ := m.handleSearchKey(tc.msg)
			got := upd.(Model)
			if got.searchMode {
				t.Fatalf("%s did not end search mode", tc.name)
			}
			if got.inputValue != line {
				t.Fatalf("%s changed input to %q", tc.name, got.inputValue)
			}
			if want := wantCursorFor(tc.msg); got.inputCursor != want {
				t.Fatalf("%s cursor = %d, want %d (motion from match start)", tc.name, got.inputCursor, want)
			}
		})
	}

	// Non-motion search keys must still be handled by the search: C-r steps to
	// the previous occurrence instead of ending the search.
	t.Run("ctrl+r_still_searches", func(t *testing.T) {
		m := newModel()
		upd, _ := m.handleSearchKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
		got := upd.(Model)
		if !got.searchMode {
			t.Fatalf("C-r ended search mode")
		}
		if want := len("this is a "); got.inputCursor != want {
			t.Fatalf("C-r cursor = %d, want %d (first occurrence)", got.inputCursor, want)
		}
	})
}

// TestReverseSearchFindsRightmostInCurrentLine: typing C-r then a pattern that
// appears twice in the current line must land on the occurrence closest to the
// cursor going backward (the rightmost), not the first one.
func TestReverseSearchFindsRightmostInCurrentLine(t *testing.T) {
	line := "this is a test and also a test"
	m := Model{inputValue: line, inputCursor: len(line)}

	m = typeSearch(m, true, "test")

	want := len("this is a test and also a ") // start of the second "test" (26)
	if m.inputCursor != want {
		t.Fatalf("reverse search cursor = %d, want %d (second occurrence)", m.inputCursor, want)
	}
	if m.searchIdx != -1 {
		t.Fatalf("expected match on current line (searchIdx -1), got %d", m.searchIdx)
	}
}

// TestSearchStepsBetweenOccurrencesWithinCurrentLine: after C-r lands on the
// rightmost "test", C-r again steps left to the first "test"; C-s then steps
// back right to the second — staying within the current line rather than
// jumping to an earlier history message.
func TestSearchStepsBetweenOccurrencesWithinCurrentLine(t *testing.T) {
	line := "this is a test and also a test"
	m := Model{
		inputValue:  line,
		inputCursor: len(line),
		history:     []string{"an earlier test message"},
	}

	m = typeSearch(m, true, "test")
	second := len("this is a test and also a ") // 26
	first := len("this is a ")                  // 10
	if m.inputCursor != second {
		t.Fatalf("initial C-r cursor = %d, want %d", m.inputCursor, second)
	}

	// C-r again: step left within the line to the first occurrence.
	m.searchBack = true
	m = m.searchStep(-1)
	if m.searchIdx != -1 || m.inputCursor != first {
		t.Fatalf("second C-r = (idx %d, cur %d), want (idx -1, cur %d)", m.searchIdx, m.inputCursor, first)
	}

	// C-s: step right within the line back to the second occurrence — must not
	// jump to history.
	m.searchBack = false
	m = m.searchStep(1)
	if m.searchIdx != -1 || m.inputCursor != second {
		t.Fatalf("C-s = (idx %d, cur %d), want (idx -1, cur %d)", m.searchIdx, m.inputCursor, second)
	}
}

// TestReverseSearchFallsThroughToHistory: when the current line has no match,
// reverse search drops into history, newest first, landing on the rightmost
// occurrence within that line.
func TestReverseSearchFallsThroughToHistory(t *testing.T) {
	m := Model{
		inputValue:  "nothing here",
		inputCursor: len("nothing here"),
		history:     []string{"old foo", "newer foo and foo"},
	}

	m = typeSearch(m, true, "foo")

	if m.searchIdx != 1 {
		t.Fatalf("expected newest matching history item (idx 1), got %d", m.searchIdx)
	}
	want := len("newer foo and ") // rightmost "foo" (14)
	if m.inputCursor != want {
		t.Fatalf("history match cursor = %d, want %d", m.inputCursor, want)
	}
}
