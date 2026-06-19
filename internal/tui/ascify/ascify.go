// Package ascify converts Unicode characters to ASCII approximations.
// Lily only supports ASCII, so this maps ISO 8859-1 and common HTML entities
// to their nearest ASCII representations.
package ascify

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

// String converts a full string to ASCII, replacing unmappable characters
// with placeholders or stripping them based on StrictMode.
func String(s string) string {
	var result []rune
	for _, r := range s {
		if ascii, ok := Ascify(r); ok {
			result = append(result, []rune(ascii)...)
		} else {
			// Character can't be converted; for now, keep it as-is and let
			// the server reject it if it's truly unsupported
			result = append(result, r)
		}
	}
	return string(result)
}
