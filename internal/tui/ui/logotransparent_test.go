package ui

import (
	"image/color"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// visibleColors renders the art and reports how many visible colours — cell
// backgrounds, and foregrounds where a glyph is actually drawn — are darker
// than the given luminance.
func visibleColors(t *testing.T, art string, bgLum float64) (darker, total int) {
	t.Helper()
	const w, h = 120, 20
	em := vt.NewEmulator(w, h)
	go func() { _, _ = io.Copy(io.Discard, em) }()
	if _, err := em.WriteString(strings.ReplaceAll(art, "\n", "\r\n")); err != nil {
		t.Fatalf("emulator: %v", err)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := em.CellAt(x, y)
			if c == nil {
				continue
			}
			drawn := c.Content != "" && c.Content != " "
			for _, pair := range []struct {
				col     color.Color
				visible bool
			}{{c.Style.Bg, true}, {c.Style.Fg, drawn}} {
				if pair.col == nil || !pair.visible {
					continue
				}
				r, g, b, _ := pair.col.RGBA()
				total++
				if luminance8(uint8(r>>8), uint8(g>>8), uint8(b>>8)) < bgLum-0.5 {
					darker++
				}
			}
		}
	}
	return
}

func luminance8(r, g, b uint8) float64 {
	return 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
}

func TestTransparentBackgroundRewrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"black background becomes default", "\x1b[48;5;16mX", "\x1b[49mX"},
		{"other backgrounds are left alone", "\x1b[48;5;232mX", "\x1b[48;5;232mX"},
		{"black foreground is left alone", "\x1b[38;5;16mX", "\x1b[38;5;16mX"},
		// The whole sequence goes, rather than becoming an empty ESC[m: that
		// would mean SGR 0 and reset colours set earlier in the same run.
		{"reverse video is dropped entirely", "\x1b[7mX", "X"},
		{"reverse does not reset a preceding colour", "\x1b[38;5;9m\x1b[7mX", "\x1b[38;5;9mX"},

		// The art's commonest shape: a foreground and a black background set
		// together. Only the background half may change.
		{"mixed run", "\x1b[38;5;1;48;5;16m ", "\x1b[38;5;1;49m "},
		{"reverse alongside colours", "\x1b[7;38;5;16mX", "\x1b[38;5;16mX"},

		// Truecolour black is a deliberate colour choice, not the palette's
		// "empty", so it is preserved.
		{"truecolour is untouched", "\x1b[48;2;0;0;0mX", "\x1b[48;2;0;0;0mX"},

		{"plain text", "hello", "hello"},
		{"reset passes through", "\x1b[0mX", "\x1b[0mX"},
		{"non-SGR sequences pass through", "\x1b[2J\x1b[48;5;16mX", "\x1b[2J\x1b[49mX"},
		{"empty SGR passes through", "\x1b[mX", "\x1b[mX"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := transparentBackground(tc.in); got != tc.want {
				t.Errorf("transparentBackground(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// Rewriting must not disturb the glyphs — only their colours.
func TestTransparentBackgroundPreservesText(t *testing.T) {
	stripped := func(s string) string {
		var b strings.Builder
		for {
			i := strings.Index(s, "\x1b[")
			if i < 0 {
				b.WriteString(s)
				return b.String()
			}
			b.WriteString(s[:i])
			j := i + 2
			for j < len(s) && s[j] != 'm' && s[j] != 'J' && s[j] != 'H' {
				j++
			}
			s = s[min(j+1, len(s)):]
		}
	}
	got := transparentBackground(logoArt)
	if stripped(got) != stripped(logoArt) {
		t.Error("the transformation changed the logo's characters, not just its colours")
	}
}

// The real assertion: render the actual logo through a terminal emulator and
// confirm no cell is left painted black, while the shading colours survive.
func TestLogoHasNoBlackBackgroundCells(t *testing.T) {
	const w, h = 120, 20

	render := func(art string) (black, colored int) {
		em := vt.NewEmulator(w, h)
		go func() { _, _ = io.Copy(io.Discard, em) }()
		if _, err := em.WriteString(strings.ReplaceAll(art, "\n", "\r\n")); err != nil {
			t.Fatalf("emulator: %v", err)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := em.CellAt(x, y)
				if c == nil || c.Style.Bg == nil {
					continue
				}
				r, g, b, _ := c.Style.Bg.RGBA()
				if r == 0 && g == 0 && b == 0 {
					black++
				} else {
					colored++
				}
			}
		}
		return
	}

	rawBlack, _ := render(logoArt)
	if rawBlack == 0 {
		t.Skip("the logo no longer paints a black background; this test has nothing to check")
	}

	gotBlack, gotColored := render(transparentBackground(logoArt))
	if gotBlack != 0 {
		t.Errorf("%d cells are still painted black (was %d) — the splash will not match the terminal",
			gotBlack, rawBlack)
	}
	// The near-black shades that model the image must survive: stripping every
	// background would flatten the art into a silhouette.
	if gotColored == 0 {
		t.Error("every background colour was removed; the logo's shading is gone")
	}
}

// The point of the lift: on a terminal whose background is not black, nothing
// in the picture may be darker than what it sits on. Transparency alone does
// not achieve that — the art paints black foregrounds too, and blanking those
// takes the petal outlines apart.
func TestLiftLeavesNothingDarkerThanTheBackground(t *testing.T) {
	bg := color.RGBA{0x16, 0x16, 0x1a, 0xff} // the browser client's own background
	bgLum := luminance8(0x16, 0x16, 0x1a)

	before, _ := visibleColors(t, transparentBackground(logoArt), bgLum)
	if before == 0 {
		t.Skip("nothing in the art is darker than this background; nothing to fix")
	}

	after, total := visibleColors(t, liftAgainst(logoArt, bg), bgLum)
	if after != 0 {
		t.Errorf("%d of %d visible colours are still darker than the background (was %d)",
			after, total, before)
	}
	// The picture must survive the treatment, not be flattened away.
	if total == 0 {
		t.Error("the lifted art draws nothing at all")
	}
}

// Lifting maps black to the background exactly and leaves white alone.
func TestLiftEndpoints(t *testing.T) {
	for _, tc := range []struct{ c, base, want uint8 }{
		{0, 0x16, 0x16},  // black becomes the background
		{255, 0x16, 255}, // white is untouched
		{0, 0, 0},        // against black, nothing moves
		{255, 0, 255},
	} {
		if got := lift(tc.c, tc.base); got != tc.want {
			t.Errorf("lift(%d, %d) = %d, want %d", tc.c, tc.base, got, tc.want)
		}
	}
}

// With no answer from the terminal there is nothing to lift against, so the
// plain black transparency is all that applies.
func TestLiftWithoutABackgroundFallsBack(t *testing.T) {
	if liftAgainst(logoArt, nil) != transparentBackground(logoArt) {
		t.Error("a nil background should fall back to plain transparency")
	}
}

// A dark picture lifted to a light background washes out to nothing, so the
// model leaves light terminals alone.
func TestIsLight(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    color.Color
		want bool
	}{
		{"black", color.RGBA{0, 0, 0, 0xff}, false},
		{"the browser default", color.RGBA{0x16, 0x16, 0x1a, 0xff}, false},
		{"white", color.RGBA{0xff, 0xff, 0xff, 0xff}, true},
		{"solarized light", color.RGBA{0xfd, 0xf6, 0xe3, 0xff}, true},
		{"mid grey leans light", color.RGBA{0x90, 0x90, 0x90, 0xff}, true},
	} {
		if got := isLight(tc.c); got != tc.want {
			t.Errorf("isLight(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
