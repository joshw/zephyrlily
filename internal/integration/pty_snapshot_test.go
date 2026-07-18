package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshw/zephyrlily/internal/lilytest"
)

// TestE2E_PTYDebugSnapshot drives the real zlily binary in a PTY under
// TERM=screen against the fake stack, logs in, runs `%debug snapshot <path>`,
// and validates the written snapshot: correct environment, a populated
// input-event ring (the typed command itself), and a non-empty renderer
// byte tail from the stdout tee.
func TestE2E_PTYDebugSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY end-to-end test in -short mode")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not available")
	}

	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	dir := t.TempDir()
	bin := buildZlilyBinary(t, dir)
	snapPath := filepath.Join(dir, "snap.txt")
	capture := filepath.Join(dir, "typescript")

	// Log in, run the snapshot command (150ms capture tick + file write need
	// a beat), then quit with double C-c.
	pipeline := `(sleep 1.5; printf 'alice\tpassword\r'; sleep 2; ` +
		`printf '%s' '%debug snapshot ` + snapPath + `'; printf '\r'; sleep 2; printf '\x03\x03'; sleep 1) | ` +
		`TERM=screen script -q ` + capture + ` sh -c 'stty rows 24 cols 80; ` + bin + ` client --proxy ` + proxyAddr + `'`
	cmd := exec.Command("sh", "-c", pipeline)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pty run: %v\n%s", err, out)
	}

	// The write happens asynchronously after the capture tick; poll briefly.
	var snap string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(snapPath); err == nil {
			snap = string(data)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if snap == "" {
		ts, _ := os.ReadFile(capture)
		t.Fatalf("snapshot file never appeared; terminal capture (%d bytes) tail:\n%q",
			len(ts), tailBytes(ts, 400))
	}

	for _, want := range []string{
		"== zlily debug snapshot v1 ==",
		"TERM=screen",
		"width=80",
		"height=24",
		"== recent input events (oldest first)",
		"key %", // the typed %debug command's first keystroke
		"== renderer output tail (base64) ==",
	} {
		if !strings.Contains(snap, want) {
			t.Errorf("snapshot missing %q", want)
		}
	}
	if strings.Contains(snap, "(no renderer tap attached)") {
		t.Error("renderer tap was not wired: snapshot has no byte tail")
	}
	// Keep the snapshot around for replay-tool development:
	// ZLILY_SNAPSHOT_KEEP=/path go test -run TestE2E_PTYDebugSnapshot, then
	// ZLILY_SNAPSHOT=/path go test ./internal/tui/ui -run TestReplaySnapshot.
	if dest := os.Getenv("ZLILY_SNAPSHOT_KEEP"); dest != "" {
		if err := os.WriteFile(dest, []byte(snap), 0o600); err != nil {
			t.Logf("could not keep snapshot at %s: %v", dest, err)
		} else {
			t.Logf("snapshot kept at %s", dest)
		}
	}
	// The login banner passed through the tee, so it must be inside the
	// base64 payload; decoding is covered by unit tests — a size check
	// suffices here.
	if idx := strings.Index(snap, "== renderer output tail (base64) =="); idx >= 0 {
		if len(snap[idx:]) < 1000 {
			t.Errorf("renderer tail section suspiciously small (%d bytes)", len(snap[idx:]))
		}
	}
}

func tailBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}
