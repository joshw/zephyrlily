package onlcr

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriterTranslatesBareNewlines(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string // written one call at a time
		want string
	}{
		{"bare LF", []string{"a\nb"}, "a\r\nb"},
		{"already CRLF is left alone", []string{"a\r\nb"}, "a\r\nb"},
		{"mixed", []string{"a\r\nb\nc"}, "a\r\nb\r\nc"},
		{"no newline", []string{"plain"}, "plain"},
		{"leading LF", []string{"\nx"}, "\r\nx"},
		{"consecutive LFs each get a CR", []string{"a\n\n\nb"}, "a\r\n\r\n\r\nb"},
		{"lone CR untouched", []string{"a\rb"}, "a\rb"},
		{"empty write", []string{""}, ""},

		// The renderer emits many small frames, so a CRLF pair landing in two
		// separate Write calls is routine — the CR must not be doubled.
		{"CRLF split across writes", []string{"a\r", "\nb"}, "a\r\nb"},
		{"LF alone after a non-CR write", []string{"a", "\nb"}, "a\r\nb"},
		{"CR alone then text", []string{"a\r", "b"}, "a\rb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			for _, s := range tc.in {
				n, err := w.Write([]byte(s))
				if err != nil {
					t.Fatalf("Write(%q): %v", s, err)
				}
				// Callers must see their own byte count, not the expanded one.
				if n != len(s) {
					t.Fatalf("Write(%q) = %d, want %d", s, n, len(s))
				}
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Writing a byte at a time must produce the same result as writing the whole
// string at once — the boundary state is the easy thing to get wrong.
func TestWriterIsChunkingInvariant(t *testing.T) {
	const src = "line\nsecond\r\nthird\n\rmixed\r\n\nend"

	var whole bytes.Buffer
	if _, err := NewWriter(&whole).Write([]byte(src)); err != nil {
		t.Fatal(err)
	}

	var byByte bytes.Buffer
	w := NewWriter(&byByte)
	for i := 0; i < len(src); i++ {
		if _, err := w.Write([]byte{src[i]}); err != nil {
			t.Fatal(err)
		}
	}

	if whole.String() != byByte.String() {
		t.Errorf("chunking changed the output:\n whole  = %q\n byByte = %q", whole.String(), byByte.String())
	}
	if strings.Contains(whole.String(), "\r\r\n") {
		t.Errorf("doubled carriage return: %q", whole.String())
	}
}

// errWriter fails every write.
type errWriter struct{ err error }

func (e errWriter) Write([]byte) (int, error) { return 0, e.err }

func TestWriterPropagatesError(t *testing.T) {
	want := bytes.ErrTooLarge
	// Both paths (translating and passthrough) must report the failure.
	for _, in := range []string{"a\nb", "plain"} {
		if _, err := NewWriter(errWriter{want}).Write([]byte(in)); err != want {
			t.Errorf("Write(%q) error = %v, want %v", in, err, want)
		}
	}
}
