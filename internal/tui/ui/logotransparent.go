package ui

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The logo is 256-colour ANSI art, and it paints its own black background
// rather than letting the terminal's show through. On a terminal whose
// background is not exactly black that leaves the splash sitting in a visible
// black rectangle — the same complaint in a browser, where xterm.js's default
// background is a dark grey, and in any terminal themed to something other than
// #000.
//
// Mapping that one colour to "default background" makes the art take the
// terminal's own background instead. Only colour 16 is remapped: the art uses
// twenty-odd near-black shades (232-237 and friends) to model the image itself,
// and stripping those would flatten it. Colour 16 is the one used for the empty
// space around and behind the logo.
//
// The art produces black two ways, and both have to be handled:
//
//	ESC[48;5;16m          an explicit black background
//	ESC[7m ESC[38;5;16m   reverse video with a black foreground, which swaps
//	                      into a black background — used for the left edge
//
// Dropping the reverse attribute turns the second into a black *foreground* on
// the default background, and the cell it applies to is a space, so nothing is
// drawn: transparent, which is the intent.
//
// Colours are only ever set here, never queried, so a straight rewrite of each
// SGR sequence is enough — no need to track terminal state. A later sequence
// that sets only a foreground inherits the default background from this one,
// which is exactly what should happen.

// Making black transparent is only half of it. The art also paints black and
// near-black *foregrounds* — 36 glyphs in pure black, and more just above it —
// so on a terminal whose background is not black the picture still shows dark
// patches where a half-block character's ink is darker than what surrounds it.
// Blanking those glyphs was tried and rejected: the petal outlines are drawn
// with them, and dropping them takes the shape apart.
//
// What works is to lift the whole picture's black point to the terminal's own
// background, so nothing in it is ever darker than what it sits on. Every
// colour is remapped
//
//	new = bg + old*(255-bg)/255
//
// which sends black to the background exactly, leaves white alone, and keeps
// every gradient in between. The result is emitted as truecolour; terminals
// without it get whatever colorprofile downsamples to.
//
// This needs the terminal's background, which arrives asynchronously (see
// tea.RequestBackgroundColor). Until it does — and on a light background, where
// lifting would wash a dark picture out to nothing — only the plain black
// transparency above applies.

const (
	// sgrBlackBG is the parameter run that sets background colour 16.
	blackBGColor = 16
	// sgrDefaultBG restores the terminal's own background.
	sgrDefaultBG = "49"
	// sgrReverse swaps foreground and background.
	sgrReverse = "7"
)

// transparentBackground rewrites ANSI art so that black backgrounds become the
// terminal's own. Text with no escape sequences is returned unchanged.
func transparentBackground(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		// Find the sequence's final byte. Only SGR ("m") is of interest; any
		// other sequence is copied through untouched.
		end := i + 2
		for end < len(s) && (s[end] == ';' || (s[end] >= '0' && s[end] <= '9')) {
			end++
		}
		if end >= len(s) || s[end] != 'm' {
			b.WriteString(s[:i+2])
			s = s[i+2:]
			continue
		}

		b.WriteString(s[:i])
		if params, keep := rewriteSGR(s[i+2 : end]); keep {
			b.WriteString("\x1b[")
			b.WriteString(params)
			b.WriteString("m")
		}
		s = s[end+1:]
	}
}

// rewriteSGR maps one SGR parameter list: black background to default, and
// reverse video dropped. keep is false when nothing is left to say, and the
// caller then emits no sequence at all — emitting an empty ESC[m would mean SGR
// 0, resetting colours the run had legitimately set.
func rewriteSGR(params string) (_ string, keep bool) {
	if params == "" {
		return "", true // ESC[m, already "reset"; leave it alone
	}
	parts := strings.Split(params, ";")
	out := make([]string, 0, len(parts))

	for i := 0; i < len(parts); i++ {
		p := parts[i]

		// Extended colour: 38/48 followed by 5;N (256-colour) or 2;R;G;B.
		if p == "38" || p == "48" {
			n, ok := extendedColorLen(parts[i:])
			if !ok {
				// Malformed; pass it through rather than guessing.
				out = append(out, p)
				continue
			}
			run := parts[i : i+n]
			if p == "48" && isColor(run, blackBGColor) {
				out = append(out, sgrDefaultBG)
			} else {
				out = append(out, run...)
			}
			i += n - 1
			continue
		}

		if p == sgrReverse {
			continue // see the package comment: this is the other way to black
		}
		out = append(out, p)
	}

	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, ";"), true
}

// extendedColorLen returns how many parameters the colour beginning at
// parts[0] occupies.
func extendedColorLen(parts []string) (int, bool) {
	if len(parts) < 2 {
		return 0, false
	}
	switch parts[1] {
	case "5": // 256-colour: 38;5;N
		if len(parts) < 3 {
			return 0, false
		}
		return 3, true
	case "2": // truecolour: 38;2;R;G;B
		if len(parts) < 5 {
			return 0, false
		}
		return 5, true
	}
	return 0, false
}

// isColor reports whether an extended-colour run names the given 256-colour
// index. Truecolour runs never match, which is correct: the art is 256-colour,
// and an RGB black is a deliberate choice rather than the palette's "empty".
func isColor(run []string, want int) bool {
	if len(run) != 3 || run[1] != "5" {
		return false
	}
	n, err := strconv.Atoi(run[2])
	return err == nil && n == want
}

// liftAgainst rewrites ANSI art so that its darkest tone becomes bg. See the
// commentary above for why this is the transformation rather than making more
// colours transparent.
func liftAgainst(s string, bg color.Color) string {
	if bg == nil {
		return transparentBackground(s)
	}
	br, bgr, bb := rgb8(bg)

	var b strings.Builder
	b.Grow(len(s) * 2) // truecolour parameters are longer than indexed ones

	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + 2
		for end < len(s) && (s[end] == ';' || (s[end] >= '0' && s[end] <= '9')) {
			end++
		}
		if end >= len(s) || s[end] != 'm' {
			b.WriteString(s[:i+2])
			s = s[i+2:]
			continue
		}

		b.WriteString(s[:i])
		if params, keep := liftSGR(s[i+2:end], br, bgr, bb); keep {
			b.WriteString("\x1b[")
			b.WriteString(params)
			b.WriteString("m")
		}
		s = s[end+1:]
	}
}

// liftSGR maps one SGR parameter list against the background.
func liftSGR(params string, br, bg, bb uint8) (_ string, keep bool) {
	if params == "" {
		return "", true
	}
	parts := strings.Split(params, ";")
	out := make([]string, 0, len(parts)*2)

	for i := 0; i < len(parts); i++ {
		p := parts[i]

		if p == "38" || p == "48" {
			n, ok := extendedColorLen(parts[i:])
			if !ok {
				out = append(out, p)
				continue
			}
			run := parts[i : i+n]
			i += n - 1

			r, g, bl, ok := runRGB(run)
			if !ok {
				out = append(out, run...)
				continue
			}
			lr, lg, lb := lift(r, br), lift(g, bg), lift(bl, bb)

			// A background that lands exactly on the terminal's own is better
			// said as "default": it survives a theme change, and costs less.
			if p == "48" && lr == br && lg == bg && lb == bb {
				out = append(out, sgrDefaultBG)
				continue
			}
			out = append(out, p, "2",
				strconv.Itoa(int(lr)), strconv.Itoa(int(lg)), strconv.Itoa(int(lb)))
			continue
		}

		if p == sgrReverse {
			continue
		}
		out = append(out, p)
	}

	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, ";"), true
}

// lift maps 0 to base and 255 to 255, linearly.
func lift(c, base uint8) uint8 {
	return base + uint8(int(c)*(255-int(base))/255)
}

// runRGB resolves an extended-colour parameter run to 8-bit RGB.
func runRGB(run []string) (r, g, b uint8, ok bool) {
	switch {
	case len(run) == 3 && run[1] == "5":
		n, err := strconv.Atoi(run[2])
		if err != nil || n < 0 || n > 255 {
			return 0, 0, 0, false
		}
		r, g, b = rgb8(ansi.IndexedColor(n))
		return r, g, b, true
	case len(run) == 5 && run[1] == "2":
		var v [3]uint8
		for i := 0; i < 3; i++ {
			n, err := strconv.Atoi(run[2+i])
			if err != nil || n < 0 || n > 255 {
				return 0, 0, 0, false
			}
			v[i] = uint8(n)
		}
		return v[0], v[1], v[2], true
	}
	return 0, 0, 0, false
}

// rgb8 reduces a color.Color to 8-bit components.
func rgb8(c color.Color) (uint8, uint8, uint8) {
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}
