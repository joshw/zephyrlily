package integration

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/lilytest"
)

// TestE2E_TmuxShrinkBoundaryColumnShift is like
// TestE2E_PTYShrinkBoundaryColumnShift but drives the real zlily binary
// inside a real tmux session instead of under script(1). script(1) is a
// passive byte recorder — nothing on the other end answers the
// terminal-capability queries (DECRQM etc.) bubbletea sends at startup, so
// every script(1)-based test may have been exercising a permanently
// different "unresponsive terminal" fallback path than a real terminal
// would. tmux is an actual terminal emulator that answers those queries,
// and it can report its own rendered screen directly via capture-pane, with
// no separate replay/emulator tooling needed. Like its script(1)
// counterpart, this is expected to stay clean: the confirmed root cause is
// a false-positive scroll-detection heuristic in mosh's own
// Display::new_frame (see forceRedraw's doc comment in
// internal/tui/ui/ui.go, and https://github.com/mobile-shell/mosh/issues/1400)
// — no real terminal emulator that isn't mosh, tmux included, has ever
// reproduced this. This test is a permanent check that zlily's own output
// stays clean under a real, capability-answering terminal.
func TestE2E_TmuxShrinkBoundaryColumnShift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY end-to-end test in -short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	dir := t.TempDir()
	bin := buildZlilyBinary(t, dir)

	const width, height = 89, 26
	session := fmt.Sprintf("zlilyrepro%d", time.Now().UnixNano())

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("tmux", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
	}
	sendLiteral := func(s string) {
		run("send-keys", "-t", session, "-l", s)
	}
	sendKey := func(k string) {
		run("send-keys", "-t", session, k)
	}
	capturePane := func() string {
		t.Helper()
		out, err := exec.Command("tmux", "capture-pane", "-p", "-t", session).CombinedOutput()
		if err != nil {
			t.Fatalf("capture-pane: %v\n%s", err, out)
		}
		return string(out)
	}

	run("new-session", "-d", "-s", session, "-x", fmt.Sprint(width), "-y", fmt.Sprint(height),
		"sh", "-c", "TERM=xterm-256color "+bin+" client --proxy "+proxyAddr)
	defer func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() }()

	time.Sleep(1500 * time.Millisecond)
	sendLiteral("alice")
	sendKey("Tab")
	sendLiteral("password")
	sendKey("Enter")
	time.Sleep(2 * time.Second)

	// width+1 'a's crosses from 1 to 2 input lines (no prompt, firstWidth ==
	// width); sent one at a time with a real gap, matching human typing.
	for i := 0; i < width+1; i++ {
		sendLiteral("a")
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	sendKey("BSpace")
	time.Sleep(200 * time.Millisecond)
	sendKey("BSpace")
	time.Sleep(500 * time.Millisecond)

	pane := capturePane()
	t.Logf("pane content:\n%s", pane)

	lines := strings.Split(pane, "\n")
	shifted := false
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " ")
		if trimmed == "" {
			continue
		}
		leading := strings.HasPrefix(trimmed, " ")
		if leading && strings.Trim(trimmed, " a") == "" && strings.Count(trimmed, "a") >= 5 {
			t.Errorf("REPRODUCED: row %d shows a leading-space shift: %q", i, trimmed)
			shifted = true
		}
	}
	if !shifted {
		t.Log("clean: no column shift detected at the shrink boundary")
	}
}
