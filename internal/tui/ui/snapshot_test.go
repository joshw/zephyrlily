package ui

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// The snapshot states the verdict itself. Making a reader diff 25 rows by eye
// is exactly the step every previous investigation got wrong.
func TestCompareHardcopy(t *testing.T) {
	frame := "alpha\nbravo\ncharlie\nstatus bar\n"

	t.Run("agreement", func(t *testing.T) {
		got := strings.Join(compareHardcopy(frame, frame, 3), "\n")
		if !strings.Contains(got, "MATCH") || strings.Contains(got, "MISMATCH") {
			t.Errorf("identical screens should report a match, got:\n%s", got)
		}
	})

	t.Run("NBSP padding and trailing blanks do not count as disagreement", func(t *testing.T) {
		// The status bar pads with U+00A0; screen writes it back as a space.
		modelFrame := "Josh   RPI | here   \n"
		fromScreen := "Josh   RPI | here\n"
		got := strings.Join(compareHardcopy(fromScreen, modelFrame, 1), "\n")
		if !strings.Contains(got, "MATCH") || strings.Contains(got, "MISMATCH") {
			t.Errorf("padding differences are not display faults, got:\n%s", got)
		}
	})

	t.Run("a display one frame behind is reported as timing, not a fault", func(t *testing.T) {
		// Exactly what a healthy snapshot looks like: the command's own echo
		// scrolled the model on by a line before the terminal caught up. Only
		// the scrolling region moves — the status bar is drawn in place.
		behind := "zero\nalpha\nbravo\nstatus bar\n"
		got := strings.Join(compareHardcopy(behind, frame, 3), "\n")
		if !strings.Contains(got, "MATCH") || !strings.Contains(got, "ordinary frame timing") {
			t.Errorf("a uniform shift should be identified as timing, got:\n%s", got)
		}
		if strings.Contains(got, "MISMATCH") {
			t.Errorf("a shift is not a mismatch, got:\n%s", got)
		}
	})

	t.Run("rows differing in place are a real mismatch", func(t *testing.T) {
		// The signature of the reported bug: one row stale while the rest agree.
		corrupt := "alpha\nbravo\nSTALE LINE\nstatus bar\n"
		got := strings.Join(compareHardcopy(corrupt, frame, 3), "\n")
		if !strings.Contains(got, "MISMATCH") {
			t.Fatalf("in-place disagreement must be flagged, got:\n%s", got)
		}
		for _, want := range []string{"row 2", "STALE LINE", "charlie"} {
			if !strings.Contains(got, want) {
				t.Errorf("the report should show %q; got:\n%s", want, got)
			}
		}
	})
}

// The comparison rides on the two halves describing the same instant, so the
// probe frame must be captured on the probe message rather than at the end.
func TestSnapshotCapturesFrameAlongsideHardcopy(t *testing.T) {
	m := newSnapshotModel(t)
	m.inputValue = "typed but not sent"

	upd, cmd := m.update(snapshotProbeMsg{path: "/tmp/x"})
	m = upd.(Model)
	if cmd == nil {
		t.Fatal("the probe should go on to capture the hardcopy")
	}
	if m.snapshotProbeFrame == "" {
		t.Fatal("the probe should record the frame believed to be on screen")
	}
	if !strings.Contains(ansi.Strip(m.snapshotProbeFrame), "typed but not sent") {
		t.Error("the probe frame should be the live frame, not a blank one")
	}

	// And it reaches the file as a verdict rather than as raw rows to eyeball.
	m.snapshotHardcopy = m.snapshotProbeFrame
	if !strings.Contains(buildSnapshot(m, nil), "MATCH") {
		t.Error("a snapshot with an agreeing hardcopy should say so")
	}
}

// The real 17:23 capture: a healthy screen whose scrollback was one line behind
// the model because the snapshot command's own echo had not yet been drawn.
// A whole-screen shift cannot describe that — the status bar does not scroll —
// so this is the case that must not be reported as a display fault.
func TestCompareHardcopyHealthyScrollbackLag(t *testing.T) {
	// 4 scrolling rows, then a status bar and an input line.
	terminal := strings.Join([]string{
		"older line", "line A", "line B", "line C",
		"Josh    RPI | here | 17:23",
		"",
	}, "\n")
	model := strings.Join([]string{
		"line A", "line B", "line C", "line D",
		"Josh    RPI | here | 17:23",
		"",
	}, "\n")

	got := strings.Join(compareHardcopy(terminal, model, 4), "\n")
	if strings.Contains(got, "MISMATCH") {
		t.Errorf("a scrollback lag with an agreeing status bar is healthy; got:\n%s", got)
	}
	if !strings.Contains(got, "status bar and input line agree") {
		t.Errorf("the verdict should say the fixed rows agree; got:\n%s", got)
	}
}

// A stale row in the fixed region is never timing — it is the signature of the
// reported bug and must be called out even when the scrollback lines up.
func TestCompareHardcopyFlagsFixedRegionDrift(t *testing.T) {
	terminal := strings.Join([]string{"a", "b", "Josh    RPI | here | 14:54", ""}, "\n")
	model := strings.Join([]string{"a", "b", "Josh    RPI | here | 14:55", ""}, "\n")

	got := strings.Join(compareHardcopy(terminal, model, 2), "\n")
	if !strings.Contains(got, "MISMATCH") {
		t.Fatalf("a stale status bar must be flagged; got:\n%s", got)
	}
	if !strings.Contains(got, "drawn in place") {
		t.Errorf("the verdict should explain why that region cannot be timing; got:\n%s", got)
	}
	if !strings.Contains(got, "row 2") {
		t.Errorf("the differing row should be reported with its absolute number; got:\n%s", got)
	}
}

// Stale text on a row the app believes it left blank is the bug's signature.
// Trimming trailing blanks before comparing would hide exactly that.
func TestCompareHardcopyCatchesUnerasedRow(t *testing.T) {
	terminal := "a\nb\nJosh    RPI\nleftover text nobody erased\n"
	model := "a\nb\nJosh    RPI\n\n"

	got := strings.Join(compareHardcopy(terminal, model, 2), "\n")
	if !strings.Contains(got, "MISMATCH") {
		t.Fatalf("stale content on a blank row must be flagged; got:\n%s", got)
	}
	if !strings.Contains(got, "leftover text nobody erased") {
		t.Errorf("the stale text should be shown; got:\n%s", got)
	}
}

// Taken verbatim from the 17:34 capture. The two sides spell the status bar's
// padding differently — zlily's frame in UTF-8, screen's hardcopy as bare 0xA0
// bytes — and a normalisation that knew only the first reported a mismatch on
// a screen that agreed exactly, because ansi.Strip then dropped the bare bytes
// as invalid UTF-8 and the padding vanished rather than differing.
func TestCompareHardcopyNBSPSpellings(t *testing.T) {
	const pad = 58
	fromScreen := "Josh" + strings.Repeat("\xa0", pad) + "RPI | here | 17:34\n"
	modelFrame := "Josh" + strings.Repeat(" ", pad) + "RPI | here | 17:34\n"

	rows := hardcopyRows(fromScreen)
	if strings.Contains(rows[0], "JoshRPI") {
		t.Fatalf("bare 0xA0 padding was dropped instead of becoming spaces: %q", rows[0])
	}
	if want := hardcopyRows(modelFrame)[0]; rows[0] != want {
		t.Errorf("the two spellings should normalise alike:\n  screen: %q\n  zlily : %q", rows[0], want)
	}

	got := strings.Join(compareHardcopy(fromScreen, modelFrame, 1), "\n")
	if !strings.Contains(got, "MATCH") || strings.Contains(got, "MISMATCH") {
		t.Errorf("identical padding must not be reported as a display fault, got:\n%s", got)
	}
}

// The workaround is a debugging knob, and a snapshot has to say which way it
// was set: taken with it on, a clean snapshot proves nothing, because the
// repaint clears any divergence whatever its cause.
func TestDebugRedrawToggle(t *testing.T) {
	m := newSnapshotModel(t)
	require.False(t, m.redrawOnShrink, "the workaround must be off by default")

	if !strings.Contains(buildSnapshot(m, nil), "redraw-on-shrink=false") {
		t.Error("the snapshot must record that the workaround was off")
	}

	m, out, _ := m.handleDebugCommand([]string{"%debug", "redraw", "on"})
	require.True(t, m.redrawOnShrink, "%debug redraw on should enable it")
	require.Contains(t, strings.Join(out, "\n"), "on")
	if !strings.Contains(buildSnapshot(m, nil), "redraw-on-shrink=true") {
		t.Error("the snapshot must record that the workaround was on")
	}

	m, out, _ = m.handleDebugCommand([]string{"%debug", "redraw", "off"})
	assert.False(t, m.redrawOnShrink)
	assert.Contains(t, strings.Join(out, "\n"), "off")

	// No argument reports without changing anything.
	m.redrawOnShrink = true
	m2, out, _ := m.handleDebugCommand([]string{"%debug", "redraw"})
	assert.True(t, m2.redrawOnShrink, "asking must not change the setting")
	assert.Contains(t, strings.Join(out, "\n"), "on")

	// A bad argument is refused rather than silently treated as off.
	m3, out, _ := m.handleDebugCommand([]string{"%debug", "redraw", "maybe"})
	assert.True(t, m3.redrawOnShrink, "an unparseable argument must not change the setting")
	assert.Contains(t, strings.Join(out, "\n"), "Usage:")
}

// M-x exists because typing "%debug snapshot" costs the input line, and the
// input line is part of what a display bug needs recorded.
func TestSnapshotKeyLeavesInputAlone(t *testing.T) {
	m := newSnapshotModel(t)
	m.inputValue = "half-typed message with a URL"
	m.inputCursor = 7
	before := len(m.output)

	upd, cmd := m.handleNormalKey(tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt})
	got := upd.(Model)

	require.NotNil(t, cmd, "M-x should start a capture")
	assert.Equal(t, "half-typed message with a URL", got.inputValue,
		"the input line must survive the key that captures it")
	assert.Equal(t, 7, got.inputCursor, "the cursor must not move either")

	// Nothing is printed at request time: appending to the scrollback would
	// scroll the region under suspicion before the terminal is measured.
	assert.Len(t, got.output, before, "the trigger must not write to the scrollback")

	// Any state from a previous snapshot is cleared, so stale measurements
	// cannot be presented as this one's.
	assert.False(t, got.cursorReport.ok)
	assert.Empty(t, got.snapshotHardcopy)
	assert.Empty(t, got.snapshotProbeFrame)
}

// The key and the command must reach the same capture, or they would drift.
func TestSnapshotKeyAndCommandAgree(t *testing.T) {
	m := newSnapshotModel(t)

	byKey, keyCmd := m.startSnapshot("")
	_, _, cmdCmd := m.handleDebugCommand([]string{"%debug", "snapshot"})
	require.NotNil(t, keyCmd)
	require.NotNil(t, cmdCmd)

	// Default path is chosen the same way for both.
	p := defaultSnapshotPath()
	assert.True(t, strings.HasSuffix(p, ".txt"), "default path should be a .txt file, got %q", p)
	assert.Contains(t, p, "zlily-debug-")
	assert.False(t, byKey.cursorReport.ok)

	// An explicit path is honoured by the command form.
	m2, _, cmd2 := m.handleDebugCommand([]string{"%debug", "snapshot", "/tmp/explicit.txt"})
	require.NotNil(t, cmd2)
	assert.False(t, m2.cursorReport.ok)
}

// M-d must keep deleting a word: rebinding it would have been destructive to
// the very input line the snapshot key exists to preserve.
func TestSnapshotKeyDoesNotStealDeleteWord(t *testing.T) {
	m := newSnapshotModel(t)
	m.inputValue = "alpha bravo"
	m.inputCursor = 0

	upd, _ := m.handleNormalKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt})
	got := upd.(Model)
	assert.NotEqual(t, "alpha bravo", got.inputValue, "M-d should still delete a word forward")
	assert.Empty(t, got.snapshotProbeFrame, "M-d must not start a snapshot")
}

// The whole M-x path, driven message by message as Update would: key press,
// probe, hardcopy, capture, file write. Guards the wiring between the four
// phases, which no single-phase test can see.
func TestSnapshotKeyWritesFileEndToEnd(t *testing.T) {
	m := newSnapshotModel(t)
	m = m.WithRendererTap(fakeTap{[]byte("bytes")})
	m.inputValue = "KEYMARKER still here"
	m.inputCursor = 3
	path := filepath.Join(t.TempDir(), "key.txt")

	// M-x, with an explicit path stood in for the default.
	m, cmd := m.startSnapshot(path)
	require.NotNil(t, cmd)

	// Phase 1: the terminal has caught up; record our frame, ask for a dump.
	upd, hcCmd := m.update(snapshotProbeMsg{path: path})
	m = upd.(Model)
	require.NotEmpty(t, m.snapshotProbeFrame, "the probe should capture the live frame")
	require.NotNil(t, hcCmd)

	// Phase 2: the dump lands (no screen here, so it reports why).
	upd, _ = m.update(hcCmd().(snapshotHardcopyMsg))
	m = upd.(Model)
	require.NotEmpty(t, m.snapshotHardcopyErr, "outside screen the reason should be recorded")

	// Phase 3: repaint done, assemble and write.
	res, ok := m.captureSnapshot(path)().(snapshotResultMsg)
	require.True(t, ok)
	require.NoError(t, res.err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)

	// The input line the key exists to preserve is in the file.
	assert.Contains(t, body, `inputvalue="KEYMARKER still here"`)
	// And so are the terminal-side measurements.
	for _, want := range []string{
		"tty size=",
		"cursor report=",
		"== screen hardcopy",
		"== hardcopy vs what zlily drew ==",
		"redraw-on-shrink=false",
	} {
		assert.Containsf(t, body, want, "snapshot should carry %q", want)
	}
}
