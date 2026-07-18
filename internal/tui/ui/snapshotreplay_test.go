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
