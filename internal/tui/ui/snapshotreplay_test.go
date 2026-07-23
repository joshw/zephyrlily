package ui

// TestReplaySnapshot is a triage tool, not a CI test: it replays the renderer
// byte tail from a %debug snapshot file through a cell-accurate terminal
// emulator and diffs the resulting screen against the snapshot's own rendered
// frame. A clean run means the bytes the renderer emitted reproduce the frame
// the app intended; a diff localizes a renderer/terminal divergence to cells.
//
// Usage:
//
//	ZLILY_SNAPSHOT=/path/to/zlily-debug-*.txt go test ./internal/tui/ui -run TestReplaySnapshot -v
//
// Notes:
//   - %debug snapshot forces a full repaint (ClearScreen) before capturing,
//     so the tail ends with a complete frame and the emulator can
//     reconstruct the whole screen even though the tail starts mid-stream.
//   - Live sessions run on a real tty (bubbletea mapNl=false: the renderer
//     emits its own \r\n), so the tail is replayed without translation.

import (
	"encoding/base64"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// clearScreenMarker is the exact byte sequence ultraviolet's TerminalRenderer
// emits for a forced full repaint (cursor home + erase entire screen; see
// (*TerminalRenderer).clearScreen). %debug snapshot triggers exactly one of
// these immediately before capturing, specifically so the tail ends in a
// self-contained frame — which also means a clean TestReplaySnapshot only
// proves the state *after* that repaint is correct. It says nothing about
// whatever was on screen in the moments before, which is usually the part a
// display-corruption report actually cares about.
const clearScreenMarker = "\x1b[H\x1b[2J"

// TestReplayPreClear replays the renderer tail only up to (not including) the
// LAST forced full-repaint marker in the stream — i.e. everything %debug
// snapshot's own cleanup repaint would otherwise erase from view. This is the
// raw, unmasked byte stream from the moment the bug actually happened. There
// is no "expected frame" to diff against here (the snapshot's rendered-frame
// section reflects state after the repaint), so this just dumps what a
// cell-accurate, independent terminal emulator makes of those bytes for
// visual inspection against what the user actually reported seeing.
func TestReplayPreClear(t *testing.T) {
	path := os.Getenv("ZLILY_SNAPSHOT")
	if path == "" {
		t.Skip("set ZLILY_SNAPSHOT=/path/to/snapshot to replay a debug snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snap := string(data)

	width := snapshotInt(t, snap, "width=")
	height := snapshotInt(t, snap, "height=")

	tail, err := base64.StdEncoding.DecodeString(
		strings.ReplaceAll(strings.TrimSpace(snapshotSection(t, snap, "renderer output tail (base64)")), "\n", ""))
	if err != nil {
		t.Fatalf("decode renderer tail: %v", err)
	}

	idx := strings.LastIndex(string(tail), clearScreenMarker)
	if idx < 0 {
		t.Skip("no forced-repaint marker found in tail; nothing to isolate (tail may not span back far enough, or capture predates this tool)")
	}
	pre := tail[:idx]
	t.Logf("tail: %d bytes total, %d bytes before the final forced repaint", len(tail), len(pre))

	em := vt.NewEmulator(width, height)
	go func() { _, _ = io.Copy(io.Discard, em) }()
	if _, err := em.Write(pre); err != nil {
		t.Fatalf("emulator write: %v", err)
	}

	t.Logf("screen state immediately before %%debug snapshot's masking repaint (%dx%d):", width, height)
	for y := 0; y < height; y++ {
		var sb strings.Builder
		for x := 0; x < width; x++ {
			if c := em.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		t.Logf("%2d: %q", y, strings.TrimRight(sb.String(), " "))
	}
}

// TestReplayFirstKeystroke narrows further than TestReplayPreClear: typing
// "%debug snapshot" to trigger the capture is itself 15-16 keystrokes, each
// redrawing the input line — enough to fully paint over whatever corruption
// was on screen before the first '%' was typed. This binary-searches the
// pre-repaint byte stream for the earliest prefix whose replay shows a '%' in
// the bottom row, i.e. the first frame after the very first character of
// "%debug snapshot" was typed — the earliest possible look at the display
// exactly as the user saw it, before any further keystrokes could overwrite
// the evidence.
func TestReplayFirstKeystroke(t *testing.T) {
	path := os.Getenv("ZLILY_SNAPSHOT")
	if path == "" {
		t.Skip("set ZLILY_SNAPSHOT=/path/to/snapshot to replay a debug snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snap := string(data)

	width := snapshotInt(t, snap, "width=")
	height := snapshotInt(t, snap, "height=")

	tail, err := base64.StdEncoding.DecodeString(
		strings.ReplaceAll(strings.TrimSpace(snapshotSection(t, snap, "renderer output tail (base64)")), "\n", ""))
	if err != nil {
		t.Fatalf("decode renderer tail: %v", err)
	}

	idx := strings.LastIndex(string(tail), clearScreenMarker)
	if idx < 0 {
		t.Skip("no forced-repaint marker found in tail")
	}
	pre := tail[:idx]

	screenAt := func(n int) *vt.Emulator {
		em := vt.NewEmulator(width, height)
		go func() { _, _ = io.Copy(io.Discard, em) }()
		_, _ = em.Write(pre[:n])
		return em
	}
	// The input area always occupies the bottom rows (viewport, then one
	// status row, then the input rows — see maybeResizeViewport's -1 for
	// status bar), so column 0 of the very last row is always input text,
	// never scrollback. Checking anywhere on screen was wrong: it matched a
	// coincidental '%' inside unrelated chat scrollback (a URL, most likely)
	// that had scrolled through long before the debug command was typed.
	hasPercent := func(n int) bool {
		em := screenAt(n)
		c := em.CellAt(0, height-1)
		return c != nil && c.Content == "%"
	}

	if !hasPercent(len(pre)) {
		t.Skip("no '%' at column 0 of the input row in the pre-repaint tail; the debug command text isn't in this window")
	}

	lo, hi := 0, len(pre)
	for lo < hi {
		mid := (lo + hi) / 2
		if hasPercent(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	t.Logf("first '%%' appears after byte %d of %d in the pre-repaint tail", lo, len(pre))

	em := screenAt(lo)
	t.Logf("screen state at the first keystroke of \"%%debug snapshot\" (%dx%d) — earliest visible glimpse of whatever was on screen right after the repro:", width, height)
	for y := 0; y < height; y++ {
		var sb strings.Builder
		for x := 0; x < width; x++ {
			if c := em.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		t.Logf("%2d: %q", y, strings.TrimRight(sb.String(), " "))
	}
}

// TestReplayLastCorruptedInputFrame goes one step further than
// TestReplayFirstKeystroke: the repro was paste, backspace, backspace,
// Ctrl-U (to clear the line), then typing "%debug snapshot" — and Ctrl-U's
// clear turned out to fully erase whatever was on screen, so the frame right
// after the first '%' was already clean. The actual moment of interest is
// one step earlier: the last frame where the input row still shows a run of
// the pasted 'a' characters, i.e. immediately after the second backspace and
// before Ctrl-U wiped it. Finds that by scanning backward from wherever the
// '%' search landed for the last frame containing a run of 5+ 'a's near the
// bottom of the screen (long enough to be the pasted content, not a
// coincidental "away"/"a" in the status bar or scrollback).
func TestReplayLastCorruptedInputFrame(t *testing.T) {
	path := os.Getenv("ZLILY_SNAPSHOT")
	if path == "" {
		t.Skip("set ZLILY_SNAPSHOT=/path/to/snapshot to replay a debug snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snap := string(data)

	width := snapshotInt(t, snap, "width=")
	height := snapshotInt(t, snap, "height=")

	tail, err := base64.StdEncoding.DecodeString(
		strings.ReplaceAll(strings.TrimSpace(snapshotSection(t, snap, "renderer output tail (base64)")), "\n", ""))
	if err != nil {
		t.Fatalf("decode renderer tail: %v", err)
	}

	idx := strings.LastIndex(string(tail), clearScreenMarker)
	if idx < 0 {
		t.Skip("no forced-repaint marker found in tail")
	}
	pre := tail[:idx]

	screenAt := func(n int) *vt.Emulator {
		em := vt.NewEmulator(width, height)
		go func() { _, _ = io.Copy(io.Discard, em) }()
		_, _ = em.Write(pre[:n])
		return em
	}
	// Only the bottom 3 rows: input area (1-2 rows) plus a one-row margin.
	// Restricting to near the bottom avoids matching ordinary prose in
	// scrollback, which is full of the letter 'a' but essentially never in a
	// run of 5+ consecutive 'a's.
	hasARun := func(n int) bool {
		em := screenAt(n)
		for y := height - 3; y < height; y++ {
			if y < 0 {
				continue
			}
			run := 0
			for x := 0; x < width; x++ {
				c := em.CellAt(x, y)
				ch := byte(' ')
				if c != nil && len(c.Content) == 1 {
					ch = c.Content[0]
				}
				if ch == 'a' {
					run++
					if run >= 5 {
						return true
					}
				} else {
					run = 0
				}
			}
		}
		return false
	}

	upper := len(pre)
	if hasARun(upper) {
		t.Skip("the final pre-repaint frame already shows a run of 'a's; nothing to search backward from")
	}

	const step = 2000
	n := upper
	for n > 0 && !hasARun(n) {
		n -= step
		if n < 0 {
			n = 0
		}
	}
	if !hasARun(n) {
		t.Skip("no run of 5+ 'a' characters found near the bottom of the screen anywhere in the pre-repaint tail")
	}

	lo, hi := n, n+step
	if hi > upper {
		hi = upper
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if hasARun(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	t.Logf("last frame with a run of 5+ 'a' near the input row: byte %d of %d", lo, len(pre))

	em := screenAt(lo)
	t.Logf("screen state right after the second backspace, before Ctrl-U cleared it (%dx%d):", width, height)
	for y := 0; y < height; y++ {
		var sb strings.Builder
		for x := 0; x < width; x++ {
			if c := em.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		t.Logf("%2d: %q", y, strings.TrimRight(sb.String(), " "))
	}
}

func TestReplaySnapshot(t *testing.T) {
	path := os.Getenv("ZLILY_SNAPSHOT")
	if path == "" {
		t.Skip("set ZLILY_SNAPSHOT=/path/to/snapshot to replay a debug snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snap := string(data)

	width := snapshotInt(t, snap, "width=")
	height := snapshotInt(t, snap, "height=")
	t.Logf("snapshot geometry: %dx%d", width, height)

	frame := snapshotFrameLines(t, snap)
	tail, err := base64.StdEncoding.DecodeString(
		strings.ReplaceAll(strings.TrimSpace(snapshotSection(t, snap, "renderer output tail (base64)")), "\n", ""))
	if err != nil {
		t.Fatalf("decode renderer tail: %v", err)
	}
	t.Logf("renderer tail: %d bytes", len(tail))

	em := vt.NewEmulator(width, height)
	go func() { _, _ = io.Copy(io.Discard, em) }()
	if _, err := em.Write(tail); err != nil {
		t.Fatalf("emulator write: %v", err)
	}

	diffs := 0
	for y := 0; y < height && y < len(frame); y++ {
		var sb strings.Builder
		for x := 0; x < width; x++ {
			if c := em.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		got := strings.TrimRight(sb.String(), " ")
		want := strings.TrimRight(ansi.Strip(frame[y]), " ")
		// The snapshot's status bar pads with NBSP; normalize for comparison.
		got = strings.ReplaceAll(got, "\u00a0", " ")
		want = strings.ReplaceAll(want, "\u00a0", " ")
		got = strings.TrimRight(got, " ")
		want = strings.TrimRight(want, " ")
		if got != want {
			diffs++
			t.Errorf("row %d diverges:\n emulator: %q\n intended: %q", y, got, want)
		}
	}
	if diffs == 0 {
		t.Logf("replay clean: emulator screen matches the snapshot's rendered frame (%d rows)", min(height, len(frame)))
	}
}

// snapshotSection returns the body of the named "== name ==" section.
func snapshotSection(t *testing.T, snap, name string) string {
	t.Helper()
	_, rest, found := strings.Cut(snap, "== "+name+" ==\n")
	if !found {
		t.Fatalf("snapshot has no %q section", name)
	}
	body, _, found := strings.Cut(rest, "\n== ")
	if !found {
		t.Fatalf("unterminated %q section", name)
	}
	return body
}

// snapshotInt finds the first "<key><int>" line in the geometry section.
func snapshotInt(t *testing.T, snap, key string) int {
	t.Helper()
	for _, line := range strings.Split(snapshotSection(t, snap, "geometry"), "\n") {
		if v, ok := strings.CutPrefix(line, key); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				t.Fatalf("parse %s%q: %v", key, v, err)
			}
			return n
		}
	}
	t.Fatalf("geometry section has no %q", key)
	return 0
}

// snapshotFrameLines unquotes the rendered-frame section into raw lines.
func snapshotFrameLines(t *testing.T, snap string) []string {
	t.Helper()
	var lines []string
	for _, q := range strings.Split(strings.TrimRight(snapshotSection(t, snap, "rendered frame (quoted lines)"), "\n"), "\n") {
		line, err := strconv.Unquote(q)
		if err != nil {
			t.Fatalf("unquote frame line %q: %v", q, err)
		}
		lines = append(lines, line)
	}
	return lines
}
