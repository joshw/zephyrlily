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

// staticModel draws a frame that never changes, so a ClearScreen is the only
// thing that could make the renderer write anything.
type staticModel struct{}

func (staticModel) Init() tea.Cmd                         { return nil }
func (m staticModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

func (staticModel) View() tea.View {
	v := tea.NewView("hello\nworld")
	v.AltScreen = true
	return v
}

// bytesAfterClear runs a program, lets its first frame settle, sends
// ClearScreen with nothing else changing, and returns what was written after.
func bytesAfterClear(t *testing.T) string {
	t.Helper()
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	out := &syncBuffer{}
	p := tea.NewProgram(staticModel{},
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

// C-l, the resume repaint and %debug snapshot all rely on ClearScreen
// repainting an unchanged view. bubbletea 2.0.8 skipped that flush as "no
// changes", so the clear wrote nothing (2026-10-02 snapshot: two C-l presses,
// zero bytes); 2.0.10 tracks the pending erase and draws it. This guards
// against losing that again in a future upgrade or re-vendoring.
func TestClearScreenRepaintsUnchangedView(t *testing.T) {
	got := bytesAfterClear(t)
	if !strings.Contains(got, "\x1b[2J") || !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("ClearScreen on an unchanged view did not repaint; wrote %q", got)
	}
}
