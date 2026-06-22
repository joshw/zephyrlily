package ascify

import "testing"

func TestAscifyEmoji(t *testing.T) {
	cases := map[rune]string{
		'\U0001F642': ":)",
		'\U0001F60A': ":)",
		'\U0001F600': ":D",
		'\U0001F602': ":'D",
		'\U0001F609': ";)",
		'\U0001F61B': ":P",
		'\U0001F974': ";}", // woozy (the original example, noseless)
		'\U0001F641': ":(",
		'\U0001F622': ":'(",
		'\U0001F620': ">:(",
		'\U0001F62E': ":O",
		'\U0001F63A': ":3", // cat
		'❤':          "<3",
		'\U0001F494': "</3",
		'\U0001F44D': "(y)",
		'\U0001F973': "\\o/",
	}
	for r, want := range cases {
		got, ok := Ascify(r)
		if !ok {
			t.Errorf("Ascify(%U) returned ok=false, want %q", r, want)
			continue
		}
		if got != want {
			t.Errorf("Ascify(%U) = %q, want %q", r, got, want)
		}
	}
}

func TestStringEmojiInline(t *testing.T) {
	got := String("hi 😊")
	want := "hi :)"
	if got != want {
		t.Errorf("String(%q) = %q, want %q", "hi 😊", got, want)
	}
}

func TestStringDropsVariationSelector(t *testing.T) {
	// Red heart followed by VS16 (U+FE0F) should drop the selector.
	got := String("❤️")
	want := "<3"
	if got != want {
		t.Errorf("String(heart+VS16) = %q, want %q", got, want)
	}
}

func TestStringDropsZWJ(t *testing.T) {
	got := String("a\u200db") // zero-width joiner between letters
	want := "ab"
	if got != want {
		t.Errorf("String(a+ZWJ+b) = %q, want %q", got, want)
	}
}

func TestStringNamedFallback(t *testing.T) {
	got := String("snow☃man") // U+2603 SNOWMAN, unmapped
	want := "snow[SNOWMAN]man"
	if got != want {
		t.Errorf("String(snow+snowman+man) = %q, want %q", got, want)
	}
}

func TestStringAccentsStillWork(t *testing.T) {
	Config.NoStripAccents = false
	got := String("café")
	want := "cafe"
	if got != want {
		t.Errorf("String(%q) = %q, want %q", "café", got, want)
	}
}
