package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/charmbracelet/x/vt"
	"github.com/joshw/zephyrlily/internal/ptytest"
	"github.com/joshw/zephyrlily/internal/tui/client"
)

// replayBottomRows feeds the raw renderer byte stream through a cell-accurate
// terminal emulator and returns the bottom three rows (status bar + the two
// input rows) as plain text.
//
// teatest gives the program piped (non-tty) input, so bubbletea v2 assumes a
// cooked output tty and emits bare \n expecting ONLCR (tea.go mapNl); the
// replay emulates that mapping. The stream also contains terminal queries
// (DECRQM etc.) that the emulator answers on its response pipe — it must be
// drained or WriteString deadlocks.
func replayBottomRows(t *testing.T, stream []byte, width, height int) string {
	t.Helper()
	em := vt.NewEmulator(width, height)
	go func() { _, _ = io.Copy(io.Discard, em) }()
	if _, err := em.WriteString(strings.ReplaceAll(string(stream), "\n", "\r\n")); err != nil {
		t.Fatalf("emulator write: %v", err)
	}
	row := func(y int) string {
		var sb strings.Builder
		for x := 0; x < width; x++ {
			if c := em.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		return sb.String()
	}
	return row(height-3) + "\n" + row(height-2) + "\n" + row(height-1)
}

// TestInputWrapNoResurrectedChars drives the reported field scenario: type a
// message, backspace a few characters off the end, then keep typing until the
// input line wraps onto a second row. The renderer's byte stream is replayed
// through a terminal emulator and the bottom rows are checked cell by cell:
// characters that were deleted must not reappear once the wrap reflows the
// layout (status bar and viewport shift by one row — the moment the
// resurrection was observed in the field).
func TestInputWrapNoResurrectedChars(t *testing.T) {
	const width, height = 80, 24

	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false

	// TERM=screen: the field report came from inside GNU screen, and the
	// renderer chooses its control-sequence vocabulary (ECH, VPA, …) from
	// TERM — exercise the same vocabulary the real environment gets.
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(width, height),
		teatest.WithProgramOptions(
			tea.WithColorProfile(colorprofile.TrueColor),
			tea.WithEnvironment(append(os.Environ(), "TERM=screen")),
		))
	time.Sleep(100 * time.Millisecond)

	typeRunes := func(s string) {
		for _, r := range s {
			tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
			// Space the keystrokes out so the renderer emits many small diff
			// frames (the failure mode lives in frame-to-frame diffing, not in
			// any single full paint).
			time.Sleep(5 * time.Millisecond)
		}
	}

	// 70 filler chars, then a distinctive tail that gets deleted. QJX appear
	// nowhere else on the bottom rows.
	typeRunes(strings.Repeat("a", 70) + "QJX")
	for i := 0; i < 3; i++ {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace})
		time.Sleep(5 * time.Millisecond)
	}
	// Type past the wrap boundary: the input becomes two rows and the whole
	// layout shifts.
	typeRunes(strings.Repeat("b", 25))

	// Accumulate renderer output (pre-teardown — the final quit leaves the
	// alt screen and would blank the emulator) until the emulator shows all
	// 25 b's, i.e. the wrapped input is fully painted.
	var acc []byte
	var bottom string
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		acc = append(acc, b...)
		bottom = replayBottomRows(t, acc, width, height)
		return strings.Count(bottom, "b") == 25
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(50*time.Millisecond))

	if strings.ContainsAny(bottom, "QJX") {
		t.Errorf("deleted characters resurrected after input wrap:\n%s", bottom)
	}
	if strings.Count(bottom, "a") != 70 {
		t.Errorf("original input not fully visible on input rows (want 70 a's):\n%s", bottom)
	}

	// Second opinion from the real thing: replay the same byte stream inside
	// an actual GNU screen session (4.00.03 on macOS — the terminal zlily
	// lives in) and dump screen's own window model via hardcopy. This catches
	// sequence-interpretation differences a modern emulator won't have.
	replayThroughRealScreen(t, acc, width, height)

	if err := tm.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// replayThroughRealScreen cats the raw renderer stream inside a GNU screen
// window, dumps the window with hardcopy, and asserts the input content wrote
// cleanly: all 25 b's, 70 a's, and none of the deleted QJX characters.
// Skips when screen(1) or script(1) is unavailable. The screen session needs
// an attached display for hardcopy to produce output, hence the script PTY.
func replayThroughRealScreen(t *testing.T, stream []byte, width, height int) {
	t.Helper()
	if _, err := exec.LookPath("screen"); err != nil {
		t.Skip("screen(1) not available")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not available")
	}

	dir := t.TempDir()
	streamFile := filepath.Join(dir, "stream.bin")
	if err := os.WriteFile(streamFile, stream, 0o644); err != nil {
		t.Fatal(err)
	}
	hc := filepath.Join(dir, "hardcopy.txt")
	session := fmt.Sprintf("zlilywrap%d", os.Getpid())

	// script provides the attached display; inside it, screen runs a window
	// that replays the byte stream and lingers for the hardcopy. -U forces
	// UTF-8 handling (like the real zlily-in-screen sessions); without it,
	// screen counts each UTF-8 continuation byte as a column and every
	// multi-byte glyph desyncs the replay.
	inner := fmt.Sprintf("stty rows %d cols %d; screen -U -S %s sh -c 'cat %s; sleep 15'",
		height, width, session, streamFile)
	cmd := exec.Command("sh", "-c",
		"(sleep 8) | LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 TERM=xterm-256color "+
			ptytest.ScriptInvocation("/dev/null", inner)+" >/dev/null 2>&1 &")
	if err := cmd.Run(); err != nil {
		t.Fatalf("start screen replay: %v", err)
	}
	defer func() {
		_ = exec.Command("screen", "-S", session, "-X", "quit").Run()
	}()

	// Wait for the replay to finish painting, then dump screen's window.
	// NOTE: 4.00.03's hardcopy writes non-Latin1 glyphs as their low byte
	// (U+258B ▋ becomes 0x8B, U+2574 ╴ becomes 't', U+2561 ╡ becomes 'a'!),
	// so the splash logo region is full of ASCII-looking artifacts. Only the
	// bottom rows — status bar + the two input rows, which are pure ASCII +
	// NBSP — are assertable.
	bottomRows := func(data []byte) string {
		lines := strings.Split(string(data), "\n")
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) < 3 {
			return strings.Join(lines, "\n")
		}
		return strings.Join(lines[len(lines)-3:], "\n")
	}

	deadline := time.Now().Add(6 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		_ = exec.Command("screen", "-S", session, "-X", "hardcopy", hc).Run()
		var err error
		data, err = os.ReadFile(hc)
		if err == nil && strings.Count(bottomRows(data), "b") >= 25 {
			break
		}
	}
	if len(data) == 0 {
		// Distinguish "screen can't run here" (CI runners often lack a socket
		// directory) from "session ran but hardcopy failed" (a real problem).
		ls, _ := exec.Command("screen", "-ls").CombinedOutput()
		if !strings.Contains(string(ls), session) {
			t.Skipf("screen session never started (environment limitation); screen -ls:\n%s", ls)
		}
		t.Fatalf("no hardcopy produced from running screen session %s", session)
	}

	bottom := bottomRows(data)
	if strings.ContainsAny(bottom, "QJX") {
		t.Errorf("REAL GNU screen shows resurrected deleted characters:\n%s", bottom)
	}
	if strings.Count(bottom, "b") != 25 {
		t.Errorf("REAL GNU screen: want 25 b's on input rows, got %d:\n%s", strings.Count(bottom, "b"), bottom)
	}
	if strings.Count(bottom, "a") != 70 {
		t.Errorf("REAL GNU screen: want 70 a's on input rows, got %d:\n%s", strings.Count(bottom, "a"), bottom)
	}
}
