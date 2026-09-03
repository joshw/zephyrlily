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

// Letters outside Latin-1 are folded through their Unicode decomposition
// rather than listed by hand, so the whole Latin range comes along.
func TestStringFoldsAccentsBeyondLatin1(t *testing.T) {
	Config.NoStripAccents = false
	cases := map[string]string{
		"ń":                 "n",
		"Ń":                 "N",
		"Zażółć gęślą jaźń": "Zazolc gesla jazn",
		"Łódź":              "Lodz",
		"Škoda":             "Skoda",
		"Việt Nam":          "Viet Nam",
		"Đorđe":             "Dorde",
		"Þór":               "Thor",
		"ışık":              "isik",
		"œuvre":             "oeuvre",
		"ǚ":                 "u",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

// The same fold keeps the accent as a trailing mark when accents are not
// stripped, matching the hand-written Latin-1 entries ("á" → "a'").
func TestStringNoStripAccentsBeyondLatin1(t *testing.T) {
	Config.NoStripAccents = true
	defer func() { Config.NoStripAccents = false }()
	cases := map[string]string{
		"ń":       "n'",
		"Nyström": "Nystro:m",
		"ž":       "zv",
		"e\u0301": "e'", // arrived decomposed rather than precomposed
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every flavour of Unicode space becomes a plain one instead of its name.
func TestStringNormalizesUnicodeWhitespace(t *testing.T) {
	// no-break, en, em, three-per-em, thin, hair, figure, ideographic,
	// narrow no-break, medium mathematical, and the line separator.
	in := "a\u00a0b\u2002c\u2003d\u2004e\u2009f\u200ag\u2007h\u3000i" +
		"\u202fj\u205fk\u2028l"
	want := "a b c d e f g h i j k l"
	if got := String(in); got != want {
		t.Errorf("String(%q) = %q, want %q", in, got, want)
	}
}

// Zero-width spaces stay dropped: they are not spaces to the reader.
func TestStringDropsZeroWidthSpace(t *testing.T) {
	// zero-width space, word joiner, byte-order mark
	if got := String("a\u200bb\u2060c\ufeffd"); got != "abcd" {
		t.Errorf("String(zero-width run) = %q, want %q", got, "abcd")
	}
}

// Skin-tone modifiers say nothing an emoticon can carry, so they drop and
// leave the emoji's own conversion behind.
func TestStringDropsEmojiModifiers(t *testing.T) {
	cases := map[string]string{
		"\U0001F44D\U0001F3FE":    "(y)", // thumbs up, type-4
		"\U0001F642\U0001F3FB":    ":)",
		"hi \U0001F44D\U0001F3FF": "hi (y)",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

// A keycap is an enclosing mark around a plain ASCII digit.
func TestStringKeycap(t *testing.T) {
	if got := String("1\ufe0f\u20e3"); got != "1" {
		t.Errorf("String(keycap 1) = %q, want %q", got, "1")
	}
}

// Regional indicators pair up into a country code.
func TestStringRegionalIndicators(t *testing.T) {
	if got := String("\U0001F1FA\U0001F1F8"); got != "US" {
		t.Errorf("String(US flag) = %q, want %q", got, "US")
	}
}

// Superscripts and subscripts keep the one thing they were saying, and a run
// of them is marked once rather than per digit.
func TestStringSuperAndSubscripts(t *testing.T) {
	cases := map[string]string{
		"x²":   "x^2",
		"y⁴":   "y^4",
		"z₁₀":  "z_10",
		"H₂O":  "H_2O",
		"10²³": "10^23",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

// Compatibility forms — full-width, the decorative math alphabets, ligatures,
// circled and parenthesised numbers, Roman numerals, CJK units — all fold to
// the plain characters they imitate.
func TestStringCompatibilityForms(t *testing.T) {
	cases := map[string]string{
		"Ａ１":   "A1",
		"𝐁𝐨𝐥𝐝": "Bold",
		"𝕊":    "S",
		"𝓼":    "s",
		"ﬁle":  "file",
		"①":    "1",
		"⑵":    "(2)",
		"Ⅷ":    "VIII",
		"㎏":    "kg",
		"№5":   "No5",
		"⅓":    "1/3",
		"3½":   "31/2",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

// Symbols are converted for meaning, not name.
func TestStringSymbols(t *testing.T) {
	cases := map[string]string{
		"a → b": "a -> b",
		"x ⇒ y": "x => y",
		"a ≤ b": "a <= b",
		"a ≠ b": "a != b",
		"−5":    "-5",
		"₹500":  "INR500",
		"™":     "(tm)",
		"✓ ✗ ⚠": "[ok] [x] [!]",
		"2′ 3″": "2' 3\"",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

// Pasted line art keeps its shape instead of turning into a wall of names.
func TestStringLineArt(t *testing.T) {
	cases := map[string]string{
		"┌───┬───┐": "+---+---+",
		"│ a │ b │": "| a | b |",
		"████░░░░":  "####::::",
		"▶ ▲":       "> ^",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

// Anything with no ASCII of its own still falls back to its Unicode name, and
// a script that would only half-transliterate is left to that fallback rather
// than mangled.
func TestStringKeepsNamedFallback(t *testing.T) {
	if got := String("Ω"); got != "[GREEK CAPITAL LETTER OMEGA]" {
		t.Errorf("String(omega) = %q, want the Unicode name", got)
	}
}

// Whatever path a rune takes, the result is always pure ASCII — the input
// line's wrap arithmetic assumes one byte is one column.
func TestStringIsAlwaysASCII(t *testing.T) {
	for _, in := range []string{
		"Zażółć gęślą jaźń", "🧑🏿‍💻", "𝐁𝐨𝐥𝐝", "┌─┐", "Привет", "⑵ ㎏ ₹ ✓",
		"a b　c", "1️⃣", "\U0001F1FA\U0001F1F8",
	} {
		for _, r := range String(in) {
			if r > 127 {
				t.Errorf("String(%q) left a non-ASCII rune %U", in, r)
			}
		}
	}
}
