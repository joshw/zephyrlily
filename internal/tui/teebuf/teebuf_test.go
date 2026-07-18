package teebuf

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestWriter returns a Writer over a real temp file with a small ring so
// wrap-around is cheap to exercise.
func newTestWriter(t *testing.T, ringSize int) *Writer {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return &Writer{File: f, ring: make([]byte, ringSize)}
}

func TestTailBeforeWrap(t *testing.T) {
	w := newTestWriter(t, 16)
	if _, err := w.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := string(w.Tail()); got != "abcdef" {
		t.Errorf("Tail = %q, want %q", got, "abcdef")
	}
}

func TestTailAfterWrap(t *testing.T) {
	w := newTestWriter(t, 8)
	for _, chunk := range []string{"1234", "5678", "abc"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	// 11 bytes through an 8-byte ring: the last 8 survive.
	if got, want := string(w.Tail()), "45678abc"; got != want {
		t.Errorf("Tail = %q, want %q", got, want)
	}
}

func TestOversizedWrite(t *testing.T) {
	w := newTestWriter(t, 8)
	big := strings.Repeat("x", 20) + "TAIL8888"
	if _, err := w.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	if got := string(w.Tail()); got != "TAIL8888" {
		t.Errorf("Tail = %q, want %q", got, "TAIL8888")
	}
}

func TestExactBoundaryWrite(t *testing.T) {
	w := newTestWriter(t, 8)
	if _, err := w.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if got := string(w.Tail()); got != "12345678" {
		t.Errorf("Tail = %q, want %q", got, "12345678")
	}
	if _, err := w.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if got := string(w.Tail()); got != "345678ab" {
		t.Errorf("Tail after boundary = %q, want %q", got, "345678ab")
	}
}

func TestWritesReachUnderlyingFile(t *testing.T) {
	w := newTestWriter(t, 8)
	payload := []byte("hello terminal")
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(w.File.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Errorf("underlying file = %q, want %q", data, payload)
	}
}
