package ui

import "testing"

func TestSpellCheckerDefaultAllowList(t *testing.T) {
	s := NewSpellChecker()
	for _, w := range []string{"zlily", "zephyrlily", "Zlily", "ZEPHYRLILY"} {
		if !s.CheckWord(w) {
			t.Errorf("CheckWord(%q) = false, want true (default allow list, case-insensitive)", w)
		}
	}
}

func TestSpellCheckerAllowForbidRemove(t *testing.T) {
	s := NewSpellChecker()

	// A clearly-misspelled word starts out wrong.
	if s.CheckWord("asdfqwer") {
		t.Fatal("expected nonsense word to be misspelled initially")
	}

	s.Allow("asdfqwer")
	if !s.CheckWord("Asdfqwer") {
		t.Error("allow overlay should accept the word case-insensitively")
	}

	// Forbid wins over allow and over the dictionary.
	s.Forbid("hello")
	if s.CheckWord("hello") {
		t.Error("forbid overlay should reject a dictionary-valid word")
	}

	// Forbidding a previously-allowed word moves it across.
	s.Forbid("asdfqwer")
	if s.CheckWord("asdfqwer") {
		t.Error("forbid should override a prior allow")
	}

	// Remove reverts to the dictionary default.
	if !s.Remove("hello") {
		t.Error("Remove should report the word was present")
	}
	if !s.CheckWord("hello") {
		t.Error("after remove, dictionary should accept 'hello' again")
	}
	if s.Remove("neverseen") {
		t.Error("Remove should report false for an absent word")
	}
}

func TestSpellCheckerOnOff(t *testing.T) {
	s := NewSpellChecker()
	s.Forbid("hello")
	if s.CheckWord("hello") {
		t.Fatal("precondition: forbidden word should be misspelled")
	}
	s.SetEnabled(false)
	if !s.CheckWord("hello") {
		t.Error("disabled checker should accept every word")
	}
	if s.Enabled() {
		t.Error("Enabled() should be false after SetEnabled(false)")
	}
	s.SetEnabled(true)
	if s.CheckWord("hello") {
		t.Error("re-enabled checker should honor the forbid overlay again")
	}
}

func TestSpellCheckerResetOverlays(t *testing.T) {
	s := NewSpellChecker()
	s.Allow("asdfqwer")
	s.Forbid("hello")
	s.ResetOverlays()

	if s.CheckWord("asdfqwer") {
		t.Error("reset should drop custom allowed words")
	}
	if !s.CheckWord("hello") {
		t.Error("reset should drop custom forbidden words")
	}
	if !s.CheckWord("zlily") {
		t.Error("reset should restore the default allow list")
	}
}

func TestSpellCommandHandler(t *testing.T) {
	s := NewSpellChecker()

	if got := s.HandleCommand([]string{"off"}); len(got) == 0 || s.Enabled() {
		t.Errorf("%%spell off did not disable; out=%v enabled=%v", got, s.Enabled())
	}
	if got := s.HandleCommand([]string{"on"}); len(got) == 0 || !s.Enabled() {
		t.Errorf("%%spell on did not enable; out=%v enabled=%v", got, s.Enabled())
	}

	s.HandleCommand([]string{"allow", "foo", "bar"})
	if !s.CheckWord("foo") || !s.CheckWord("bar") {
		t.Error("allow with multiple words should allow each")
	}

	s.HandleCommand([]string{"forbid", "foo"})
	if s.CheckWord("foo") {
		t.Error("forbid should move the word to the forbid overlay")
	}

	// Malformed: allow with no words returns usage, changes nothing.
	out := s.HandleCommand([]string{"allow"})
	if len(out) == 0 || out[0] != spellUsage[0] {
		t.Errorf("allow with no words should return usage, got %v", out)
	}
}
