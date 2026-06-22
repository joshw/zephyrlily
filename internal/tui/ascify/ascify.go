// Package ascify converts Unicode characters to ASCII approximations.
// Lily only supports ASCII, so this maps ISO 8859-1 and common HTML entities
// to their nearest ASCII representations.
package ascify

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/runenames"
)

// Map stores both strip (accents removed) and no-strip (accents preserved) versions
type Map struct {
	Strip   string
	NoStrip string
}

// CharMap is the master Unicode to ASCII mapping
var CharMap = map[rune]interface{}{
	// Misc symbols
	'\u20AC': "EUR",  // euro €
	'\u2013': "-",    // en dash –
	'\u2014': "--",   // em dash —
	'\u2018': "'",    // left single quote '
	'\u2019': "'",    // right single quote '
	'\u201C': "\"",   // left double quote "
	'\u201D': "\"",   // right double quote "
	'\u2026': "...",  // ellipsis …
	'\u2022': "-",    // bullet •
	'\u2020': "[t]",  // dagger †
	'\u2021': "[tt]", // double dagger ‡
	'\u2030': "%%",   // per mille ‰
	'\u2039': "<",    // single left angle ‹
	'\u203A': ">",    // single right angle ›

	// ISO 8859-1 Symbols
	'\u00A0': " ",     // non-breaking space
	'\u00A1': "!",     // inverted exclamation ¡
	'\u00A2': "cents", // cent ¢
	'\u00A3': "GBP",   // pound £
	'\u00A5': "JPY",   // yen ¥
	'\u00A6': "|",     // broken bar ¦
	'\u00A7': "S",     // section §
	'\u00A8': ":",     // diaeresis ¨
	'\u00A9': "(c)",   // copyright ©
	'\u00AB': "<<",    // left angle quote «
	'\u00AC': "!",     // negation ¬
	'\u00AD': "-",     // soft hyphen
	'\u00AE': "(r)",   // registered ®
	'\u00AF': "-",     // macron ¯
	'\u00B0': "*",     // degree °
	'\u00B1': "+/-",   // plus-minus ±
	'\u00B2': "^2",    // superscript 2 ²
	'\u00B3': "^3",    // superscript 3 ³
	'\u00B5': "u",     // micro µ
	'\u00B6': "[|P]",  // paragraph ¶
	'\u00B7': ".",     // middle dot ·
	'\u00B9': "^1",    // superscript 1 ¹
	'\u00BB': ">>",    // right angle quote »
	'\u00BC': "1/4",   // fraction 1/4 ¼
	'\u00BD': "1/2",   // fraction 1/2 ½
	'\u00BE': "3/4",   // fraction 3/4 ¾
	'\u00BF': "?",     // inverted question ¿
	'\u00D7': "x",     // multiplication ×
	'\u00F7': "/",     // division ÷

	// ISO 8859-1 Accented characters (strip vs nostrip)
	'\u00C0': &Map{"A", "A`"}, // capital A grave À
	'\u00C1': &Map{"A", "A'"}, // capital A acute Á
	'\u00C2': &Map{"A", "A^"}, // capital A circumflex Â
	'\u00C3': &Map{"A", "A~"}, // capital A tilde Ã
	'\u00C4': &Map{"A", "A:"}, // capital A umlaut Ä
	'\u00C5': &Map{"A", "A*"}, // capital A ring Å
	'\u00C6': "AE",            // capital AE Æ
	'\u00C7': &Map{"C", "C,"}, // capital C cedilla Ç
	'\u00C8': &Map{"E", "E`"}, // capital E grave È
	'\u00C9': &Map{"E", "E'"}, // capital E acute É
	'\u00CA': &Map{"E", "E^"}, // capital E circumflex Ê
	'\u00CB': &Map{"E", "E:"}, // capital E umlaut Ë
	'\u00CC': &Map{"I", "I`"}, // capital I grave Ì
	'\u00CD': &Map{"I", "I'"}, // capital I acute Í
	'\u00CE': &Map{"I", "I^"}, // capital I circumflex Î
	'\u00CF': &Map{"I", "I:"}, // capital I umlaut Ï
	'\u00D1': &Map{"N", "N~"}, // capital N tilde Ñ
	'\u00D2': &Map{"O", "O`"}, // capital O grave Ò
	'\u00D3': &Map{"O", "O'"}, // capital O acute Ó
	'\u00D4': &Map{"O", "O^"}, // capital O circumflex Ô
	'\u00D5': &Map{"O", "O~"}, // capital O tilde Õ
	'\u00D6': &Map{"O", "O:"}, // capital O umlaut Ö
	'\u00D8': "0",             // capital O slash Ø
	'\u00D9': &Map{"U", "U`"}, // capital U grave Ù
	'\u00DA': &Map{"U", "U'"}, // capital U acute Ú
	'\u00DB': &Map{"U", "U^"}, // capital U circumflex Û
	'\u00DC': &Map{"U", "U:"}, // capital U umlaut Ü
	'\u00DD': &Map{"Y", "Y'"}, // capital Y acute Ý
	'\u00DF': "B",             // German sharp s ß
	'\u00E0': &Map{"a", "a`"}, // small a grave à
	'\u00E1': &Map{"a", "a'"}, // small a acute á
	'\u00E2': &Map{"a", "a^"}, // small a circumflex â
	'\u00E3': &Map{"a", "a~"}, // small a tilde ã
	'\u00E4': &Map{"a", "a:"}, // small a umlaut ä
	'\u00E5': &Map{"a", "a*"}, // small a ring å
	'\u00E6': "ae",            // small ae æ
	'\u00E7': &Map{"c", "c,"}, // small c cedilla ç
	'\u00E8': &Map{"e", "e`"}, // small e grave è
	'\u00E9': &Map{"e", "e'"}, // small e acute é
	'\u00EA': &Map{"e", "e^"}, // small e circumflex ê
	'\u00EB': &Map{"e", "e:"}, // small e umlaut ë
	'\u00EC': &Map{"i", "i`"}, // small i grave ì
	'\u00ED': &Map{"i", "i'"}, // small i acute í
	'\u00EE': &Map{"i", "i^"}, // small i circumflex î
	'\u00EF': &Map{"i", "i:"}, // small i umlaut ï
	'\u00F1': &Map{"n", "n~"}, // small n tilde ñ
	'\u00F2': &Map{"o", "o`"}, // small o grave ò
	'\u00F3': &Map{"o", "o'"}, // small o acute ó
	'\u00F4': &Map{"o", "o^"}, // small o circumflex ô
	'\u00F5': &Map{"o", "o~"}, // small o tilde õ
	'\u00F6': &Map{"o", "o:"}, // small o umlaut ö
	'\u00F8': &Map{"o", "0"},  // small o slash ø
	'\u00F9': &Map{"u", "u`"}, // small u grave ù
	'\u00FA': &Map{"u", "u'"}, // small u acute ú
	'\u00FB': &Map{"u", "u^"}, // small u circumflex û
	'\u00FC': &Map{"u", "u:"}, // small u umlaut ü
	'\u00FD': &Map{"y", "y'"}, // small y acute ý
	'\u00FF': &Map{"y", "y:"}, // small y umlaut ÿ

	// Emoji smileys → noseless ASCII emoticons
	// Smiling / happy
	'\U0001F642': ":)",   // slightly smiling
	'\U0001F60A': ":)",   // smiling, smiling eyes
	'☺':          ":)",   // white smiling face
	'\U0001F60C': ":)",   // relieved
	'\U0001F600': ":D",   // grinning
	'\U0001F603': ":D",   // grinning big eyes
	'\U0001F604': ":D",   // grinning smiling eyes
	'\U0001F601': ":D",   // beaming
	'\U0001F606': "XD",   // grinning squinting
	'\U0001F605': ":D",   // grinning sweat
	'\U0001F602': ":'D",  // tears of joy
	'\U0001F923': ":'D",  // rolling on the floor
	'\U0001F607': "O:)",  // halo
	'\U0001F917': ">:D<", // hugging
	'\U0001F643': "(:",   // upside-down

	// Wink / playful / tongue
	'\U0001F609': ";)", // winking
	'\U0001F61C': ";P", // winking tongue
	'\U0001F61B': ":P", // tongue out
	'\U0001F61D': "XP", // squinting tongue
	'\U0001F60B': ":P", // savoring food
	'\U0001F92A': "%)", // zany
	'\U0001F60F': ":>", // smirking

	// Cool / nerd / love / kiss
	'\U0001F60E': "B)", // sunglasses
	'\U0001F913': "8)", // nerd
	'\U0001F9D0': ":?", // monocle
	'\U0001F60D': "<3", // heart eyes
	'\U0001F970': "<3", // smiling with hearts
	'\U0001F618': ":*", // blowing a kiss
	'\U0001F617': ":*", // kissing
	'\U0001F619': ":*", // kissing smiling eyes
	'\U0001F61A': ":*", // kissing closed eyes

	// Neutral / thinking / skeptical
	'\U0001F610': ":|",  // neutral
	'\U0001F611': ":|",  // expressionless
	'\U0001F636': ":x",  // no mouth
	'\U0001F910': ":x",  // zipper mouth
	'\U0001F914': ":?",  // thinking
	'\U0001F644': "9_9", // rolling eyes
	'\U0001F612': ":/",  // unamused
	'\U0001F615': ":/",  // confused

	// Sleepy / sick / woozy
	'\U0001F634': "|)", // sleeping
	'\U0001F62A': "|)", // sleepy
	'\U0001F974': ";}", // woozy
	'\U0001F635': "%)", // dizzy
	'\U0001F922': ":&", // nauseated
	'\U0001F92E': ":&", // vomiting
	'\U0001F912': ":(", // thermometer
	'\U0001F915': ":(", // head bandage
	'\U0001F637': ":x", // medical mask

	// Sad / crying / worried
	'\U0001F641': ":(",  // slightly frowning
	'☹':          ":(",  // white frowning face
	'\U0001F61E': ":(",  // disappointed
	'\U0001F614': ":(",  // pensive
	'\U0001F61F': ":(",  // worried
	'\U0001F97A': ":(",  // pleading
	'\U0001F622': ":'(", // crying
	'\U0001F62D': ":'(", // loudly crying
	'\U0001F625': ":'(", // sad but relieved
	'\U0001F613': ":'(", // downcast sweat
	'\U0001F623': ">_<", // persevering
	'\U0001F616': ">_<", // confounded

	// Fear / shock / surprise
	'\U0001F62E': ":O", // open mouth
	'\U0001F62F': ":O", // hushed
	'\U0001F632': ":O", // astonished
	'\U0001F92F': ":O", // mind blown
	'\U0001F633': ":O", // flushed
	'\U0001F628': "D:", // fearful
	'\U0001F627': "D:", // anguished
	'\U0001F626': "D:", // frowning open mouth
	'\U0001F629': "D:", // weary
	'\U0001F62B': "D:", // tired
	'\U0001F631': "D:", // screaming in fear

	// Angry / devil
	'\U0001F620': ">:(", // angry
	'\U0001F621': ">:(", // pouting
	'\U0001F92C': ">:(", // cursing
	'\U0001F624': ">:(", // steam from nose
	'\U0001F608': ">:)", // smiling with horns
	'\U0001F47F': ">:(", // imp / angry with horns

	// Grimace
	'\U0001F62C': ":E", // grimacing

	// Cat faces
	'\U0001F63A': ":3",  // smiling cat
	'\U0001F638': ":3",  // grinning cat
	'\U0001F639': ":'3", // cat tears of joy
	'\U0001F63B': ":3",  // heart-eyes cat
	'\U0001F63C': ":3",  // wry cat
	'\U0001F63D': ":3",  // kissing cat

	// Hearts / extras
	'❤':          "<3",   // red heart
	'\U0001F495': "<3",   // two hearts
	'\U0001F496': "<3",   // sparkling heart
	'\U0001F497': "<3",   // growing heart
	'\U0001F9E1': "<3",   // orange heart
	'\U0001F49B': "<3",   // yellow heart
	'\U0001F49A': "<3",   // green heart
	'\U0001F499': "<3",   // blue heart
	'\U0001F49C': "<3",   // purple heart
	'\U0001F494': "</3",  // broken heart
	'\U0001F44D': "(y)",  // thumbs up
	'\U0001F44E': "(n)",  // thumbs down
	'\U0001F973': "\\o/", // partying face
}

// Config for ascify behavior
var Config = struct {
	NoStripAccents bool
}{
	NoStripAccents: false,
}

// Ascify converts a Unicode rune to its ASCII equivalent.
// Returns the ASCII string and a boolean indicating if a conversion was made.
func Ascify(r rune) (string, bool) {
	// ASCII characters pass through unchanged
	if r < 128 {
		return string(r), true
	}

	mapping, exists := CharMap[r]
	if !exists {
		return "", false
	}

	// Handle different mapping types
	switch m := mapping.(type) {
	case string:
		return m, true
	case *Map:
		if Config.NoStripAccents {
			return m.NoStrip, true
		}
		return m.Strip, true
	}

	return "", false
}

// String converts a full string to ASCII. Mapped characters use their ASCII
// equivalent; anything else falls back so the result is always pure ASCII:
// invisible formatting (zero-width joiners, combining marks, variation
// selectors) is dropped, and any remaining non-ASCII character is rendered as
// its Unicode name in brackets (e.g. "[SNOWMAN]"), or "[U+XXXX]" if unnamed.
func String(s string) string {
	var b strings.Builder
	for _, r := range s {
		if ascii, ok := Ascify(r); ok {
			b.WriteString(ascii)
			continue
		}
		// Drop invisible joiners / combining marks / variation selectors.
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if name := runenames.Name(r); name != "" {
			b.WriteString("[" + name + "]")
		} else {
			b.WriteString(fmt.Sprintf("[U+%04X]", r))
		}
	}
	return b.String()
}
