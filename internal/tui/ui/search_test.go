package ui

import "testing"

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
