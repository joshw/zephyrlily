package ui

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// syncBuffer is a bytes.Buffer safe to read while the program writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// parityModel draws a fixed frame, nudged exactly as Model.View does, so the
// test can watch what the real renderer makes of a ClearScreen on its own.
type parityModel struct {
	nudge  bool // apply the workaround at all
	parity bool
}

func (p parityModel) Init() tea.Cmd { return nil }

func (p parityModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if p.nudge && erasesScreen(msg) {
		p.parity = !p.parity
	}
	return p, nil
}

func (p parityModel) View() tea.View {
	content := "hello\nworld"
	if p.parity {
		content += repaintNudge
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// bytesAfterClear runs a program, lets its first frame settle, sends
// ClearScreen with nothing else changing, and returns what was written after.
func bytesAfterClear(t *testing.T, m parityModel) string {
	t.Helper()
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	out := &syncBuffer{}
	p := tea.NewProgram(m,
		tea.WithContext(t.Context()),
		tea.WithInput(pr),
		tea.WithOutput(out),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithoutSignals(),
		tea.WithWindowSize(80, 24),
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.Run()
	}()
	defer func() { p.Quit(); <-done }()

	time.Sleep(150 * time.Millisecond)
	before := len(out.String())
	p.Send(tea.ClearScreen())
	time.Sleep(150 * time.Millisecond)
	return out.String()[before:]
}

// bubbletea skips a flush whose view is unchanged, and an erase only leaves a
// repaint pending, so a bare ClearScreen writes nothing. That is why C-l could
// not recover a display wiped downstream of zlily (2026-10-02 snapshot: two
// C-l presses, zero bytes). The control case pins the upstream behavior, so
// this test notices if bubbletea ever fixes it and the nudge can go.
func TestClearScreenRepaintsUnchangedView(t *testing.T) {
	if got := bytesAfterClear(t, parityModel{nudge: false}); strings.Contains(got, "hello") {
		t.Logf("bubbletea now repaints a bare ClearScreen (%q); repaintParity may be unnecessary", got)
	}

	got := bytesAfterClear(t, parityModel{nudge: true})
	if !strings.Contains(got, "\x1b[2J") || !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("ClearScreen with the nudge did not repaint; wrote %q", got)
	}
}

func TestModelNudgesViewOnScreenErase(t *testing.T) {
	m := newSnapshotModel(t)
	base := m.View().Content

	upd, _ := m.Update(tea.ClearScreen())
	m = upd.(Model)
	if got := m.View().Content; got != base+repaintNudge {
		t.Fatalf("ClearScreen should change the frame by exactly the nudge:\nbefore %q\nafter  %q", base, got)
	}

	// A resize also erases; its frame changes for other reasons as well, so
	// only the flip itself is checked.
	upd, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if upd.(Model).repaintParity == m.repaintParity {
		t.Error("WindowSizeMsg did not flip repaintParity")
	}
	m = upd.(Model)

	upd, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if upd.(Model).repaintParity != m.repaintParity {
		t.Error("an ordinary key flipped repaintParity")
	}
}
