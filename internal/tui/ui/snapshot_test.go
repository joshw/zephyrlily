package ui

import (
	"encoding/base64"
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
