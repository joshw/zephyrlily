package ui

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/tui/client"
)

type fakeTap struct{ tail []byte }

func (f fakeTap) Tail() []byte { return f.tail }

func newSnapshotModel(t *testing.T) Model {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false
	m.width, m.height = 80, 24
	return m
}

func TestBuildSnapshotSections(t *testing.T) {
	m := newSnapshotModel(t)
	m.inputValue = "hello wor"
	m.inputCursor = 9
	m.recordKeyEvent(tea.KeyPressMsg{Code: 'h', Text: "h"})
	m.recordEvent("resize %dx%d (was %dx%d)", 80, 24, 0, 0)
	m.recordMsgMeta("recv type=text id=7")

	tail := []byte("\x1b[2J\x1b[Hfake frame bytes")
	snap := buildSnapshot(m, fakeTap{tail}.Tail())

	for _, want := range []string{
		"== zlily debug snapshot v1 ==",
		"PRIVACY:",
		"== build ==",
		"== environment ==",
		"TERM=",
		"== geometry ==",
		"width=80",
		"height=24",
		"== input state ==",
		`inputvalue="hello wor"`,
		"inputcursor=9 len=9",
		"== responsiveness ==",
		"lifetime:",
		"trend (p95 latency per window",
		"== recent input events (oldest first)",
		"key h",
		"resize 80x24 (was 0x0)",
		"== recent proxy traffic (metadata only, oldest first)",
		"recv type=text id=7",
		"== scrollback metadata ==",
		"== rendered frame (quoted lines) ==",
		"== renderer output tail (base64) ==",
		"== goroutines ==",
		"== end of snapshot ==",
	} {
		if !strings.Contains(snap, want) {
			t.Errorf("snapshot missing %q", want)
		}
	}

	// The base64 tail round-trips to the exact renderer bytes.
	enc := extractBase64Section(t, snap)
	dec, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("tail base64: %v", err)
	}
	if string(dec) != string(tail) {
		t.Errorf("tail round-trip = %q, want %q", dec, tail)
	}
}

func TestBuildSnapshotWithoutTap(t *testing.T) {
	m := newSnapshotModel(t)
	snap := buildSnapshot(m, nil)
	if !strings.Contains(snap, "(no renderer tap attached)") {
		t.Error("nil tail should be reported explicitly")
	}
}

func TestRingOrderAndCapacity(t *testing.T) {
	r := newRing(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		r.add(s)
	}
	got := []string{}
	for _, e := range r.entries() {
		got = append(got, e.desc)
	}
	if strings.Join(got, "") != "cde" {
		t.Errorf("ring entries = %v, want [c d e]", got)
	}
}

func TestDebugCommandUsage(t *testing.T) {
	m := newSnapshotModel(t)
	_, out, cmd, recognized := m.applyLocalCommand("%debug")
	if !recognized {
		t.Fatalf("bare debug command not recognized as a local command")
	}
	if cmd != nil {
		t.Errorf("bare debug command should not issue a command")
	}
	if len(out) == 0 || !strings.Contains(out[0], "Usage: %debug snapshot") {
		t.Errorf("bare %%debug should print usage, got %v", out)
	}
}

func TestDebugSnapshotWritesFile(t *testing.T) {
	m := newSnapshotModel(t)
	m = m.WithRendererTap(fakeTap{[]byte("bytes")})
	m.inputValue = "SNAPMARKER"
	path := filepath.Join(t.TempDir(), "snap.txt")

	upd, out, cmd, recognized := m.applyLocalCommand("%debug snapshot " + path)
	if !recognized || cmd == nil {
		t.Fatalf("recognized=%v cmd=%v", recognized, cmd)
	}
	if out != nil {
		t.Errorf("no immediate output expected, got %v", out)
	}
	m = upd

	// The command is a Sequence: ClearScreen, then (after a tick) the
	// capture message. Drive the capture directly as Update would.
	writeCmd := m.captureSnapshot(path)
	res, ok := writeCmd().(snapshotResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("write result = %+v", res)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `inputvalue="SNAPMARKER"`) {
		t.Error("snapshot file missing input state")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("snapshot file mode = %v, want 0600", fi.Mode().Perm())
	}
}

// extractBase64Section pulls the base64 payload out of the renderer-tail
// section of a snapshot document.
func extractBase64Section(t *testing.T, snap string) string {
	t.Helper()
	_, rest, found := strings.Cut(snap, "== renderer output tail (base64) ==\n")
	if !found {
		t.Fatal("no renderer tail section")
	}
	body, _, found := strings.Cut(rest, "\n== ")
	if !found {
		t.Fatal("unterminated renderer tail section")
	}
	return strings.ReplaceAll(strings.TrimSpace(body), "\n", "")
}

// The kernel's terminal size is printed next to the model's, and a
// disagreement is called out rather than left for the reader to spot: a size
// desync means every row the app addressed was off by the difference.
func TestSnapshotReportsTTYSizeAgainstModel(t *testing.T) {
	m := newSnapshotModel(t)

	// Under `go test` stdout is not a terminal, so the size is unavailable —
	// which must be said plainly rather than silently omitted or guessed.
	snap := buildSnapshot(m, nil)
	if !strings.Contains(snap, "tty size=") {
		t.Fatal("geometry section should always report the tty size, even when it cannot be read")
	}
	if !strings.Contains(snap, "unavailable") {
		t.Errorf("with no tty, the size line should say so; got:\n%s", geometryOf(t, snap))
	}

	// The mismatch wording is what a reader scans for, so pin it.
	for _, want := range []string{"MISMATCH", "model believes"} {
		if !strings.Contains(mismatchTemplate, want) {
			t.Errorf("the mismatch line should contain %q", want)
		}
	}
}

// mismatchTemplate mirrors the format string used for a size disagreement, so
// the test fails if the wording a reader greps for is changed.
const mismatchTemplate = "tty size=%dx%d  *** MISMATCH: model believes %dx%d ***\n"

func geometryOf(t *testing.T, snap string) string {
	t.Helper()
	i := strings.Index(snap, "== geometry ==")
	if i < 0 {
		return snap
	}
	rest := snap[i:]
	if j := strings.Index(rest, "\n== "); j > 0 {
		return rest[:j]
	}
	return rest
}

func TestSnapshotReportsCursorPosition(t *testing.T) {
	t.Run("no answer from the terminal", func(t *testing.T) {
		m := newSnapshotModel(t)
		snap := buildSnapshot(m, nil)
		if !strings.Contains(snap, "cursor report=<no answer from terminal>") {
			t.Errorf("an unanswered query should be reported, not omitted:\n%s", geometryOf(t, snap))
		}
	})

	t.Run("answered", func(t *testing.T) {
		m := newSnapshotModel(t)
		upd, _ := m.update(tea.CursorPositionMsg{X: 4, Y: 22})
		m = upd.(Model)
		if !m.cursorReport.ok {
			t.Fatal("a CursorPositionMsg should be recorded")
		}
		snap := buildSnapshot(m, nil)
		// Reported 1-based, because that is how the terminal's own escape
		// sequences count and how every other row/col in a bug report reads.
		if !strings.Contains(snap, "cursor report=row 23 col 5") {
			t.Errorf("cursor should be reported 1-based; got:\n%s", geometryOf(t, snap))
		}
	})
}

func TestSnapshotIncludesScreenHardcopy(t *testing.T) {
	t.Run("captured", func(t *testing.T) {
		m := newSnapshotModel(t)
		upd, _ := m.update(snapshotHardcopyMsg{path: "/tmp/x", text: "row one\nrow two\n"})
		m = upd.(Model)
		snap := buildSnapshot(m, nil)
		if !strings.Contains(snap, "== screen hardcopy") {
			t.Fatal("snapshot should carry a hardcopy section")
		}
		if !strings.Contains(snap, "row one\nrow two\n") {
			t.Error("the hardcopy text should appear verbatim")
		}
	})

	t.Run("unavailable reasons are recorded, not swallowed", func(t *testing.T) {
		m := newSnapshotModel(t)
		upd, _ := m.update(snapshotHardcopyMsg{path: "/tmp/x", err: errNotInScreen})
		m = upd.(Model)
		snap := buildSnapshot(m, nil)
		if !strings.Contains(snap, "unavailable") || !strings.Contains(snap, "STY unset") {
			t.Errorf("why there is no hardcopy matters as much as having one; got:\n%s", snap)
		}
	})
}

// screenHardcopy must not shell out at all when there is no screen session to
// ask — the snapshot path runs on a user keystroke.
func TestScreenHardcopyRequiresScreen(t *testing.T) {
	t.Setenv("STY", "")
	if _, err := screenHardcopy(); !errors.Is(err, errNotInScreen) {
		t.Errorf("err = %v, want errNotInScreen", err)
	}
}

// The terminal is measured BEFORE the repaint. Capturing after it would record
// a healthy screen every time, because the repaint is what clears the
// corruption a snapshot is taken to document.
func TestSnapshotMeasuresTerminalBeforeRepainting(t *testing.T) {
	m := newSnapshotModel(t)
	m.cursorReport = cursorReport{x: 1, y: 1, ok: true}
	m.snapshotHardcopy = "stale from a previous snapshot"

	m2, out, cmd := m.handleDebugCommand([]string{"%debug", "snapshot", filepath.Join(t.TempDir(), "s.txt")})
	if cmd == nil {
		t.Fatal("the command should start the capture")
	}
	if out != nil {
		t.Errorf("no synchronous output expected, got %v", out)
	}
	// State from any previous snapshot is cleared, so a stale hardcopy or
	// cursor report can never be presented as this snapshot's measurement.
	if m2.cursorReport.ok {
		t.Error("a previous cursor report must not survive into a new snapshot")
	}
	if m2.snapshotHardcopy != "" || m2.snapshotHardcopyErr != "" {
		t.Error("a previous hardcopy must not survive into a new snapshot")
	}
}
