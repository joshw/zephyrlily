package inputguard

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncompleteTailStart(t *testing.T) {
	const esc = "\x1b"
	tests := []struct {
		name string
		in   string
		want int // -1 means "ends on a boundary"
	}{
		{"empty", "", -1},
		{"plain text", "hello", -1},
		{"complete sgr mouse", esc + "[<64;14;14M", -1},
		{"complete sgr release", esc + "[<0;5;5m", -1},
		{"complete arrow", esc + "[A", -1},
		{"complete ss3", esc + "OP", -1},
		{"text then complete mouse", "abc" + esc + "[<64;1;1M", -1},
		// Splits: the trailing sequence is unterminated.
		{"split mouse mid-params", esc + "[<64;14;1", 0},
		{"split mouse after text", "abc" + esc + "[<64;14;1", 3},
		{"split right after csi", esc + "[", 0},
		{"lone trailing esc", "abc" + esc, 3},
		{"split ss3", esc + "O", 0},
		// A complete sequence followed by a split one: hold from the second ESC.
		{"complete then split", esc + "[A" + esc + "[<64;1", 3},
		// alt+key is two bytes and complete.
		{"alt key", esc + "b", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, incompleteTailStart([]byte(tt.in)))
		})
	}
}

// chunkReader hands out a fixed script of byte slices, one per Read call, to
// simulate the OS delivering input in particular chunks.
type chunkReader struct {
	chunks [][]byte
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := c.chunks[0]
	n := copy(p, chunk)
	if n < len(chunk) {
		c.chunks[0] = chunk[n:]
	} else {
		c.chunks = c.chunks[1:]
	}
	return n, nil
}

// drain reads from r until EOF using a fixed-size buffer (mimicking Bubble
// Tea's 256-byte reads) and returns the concatenated output.
func drainWith(t *testing.T, r *Reader, bufSize int, src *chunkReader) []byte {
	t.Helper()
	var out bytes.Buffer
	buf := make([]byte, bufSize)
	for {
		n, err := r.read(buf, src.Read)
		out.Write(buf[:n])
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	return out.Bytes()
}

func TestReaderHoldsSplitSequence(t *testing.T) {
	const esc = "\x1b"
	// With a 16-byte buffer, the first chunk fills it exactly: one complete mouse
	// report (10 bytes) plus the start of a second (6 bytes), so the read splits
	// the second report. Its continuation arrives in the next chunk.
	first := esc + "[<64;1;1M" + esc + "[<64;" // 10 + 6 = 16 bytes -> full read
	second := "1;1M" + esc + "[<64;3;3M"       // completes the split report, plus one more
	want := first + second

	got := drainWith(t, New(nil), 16, &chunkReader{chunks: [][]byte{[]byte(first), []byte(second)}})

	// No bytes are lost or reordered: output equals the raw concatenation.
	assert.Equal(t, want, string(got))

	// Crucially, the byte boundaries Bubble Tea sees never split a report: every
	// emitted chunk must end at the end of a complete sequence (or carry no ESC).
	assertNoSplitChunks(t, New(nil), 16, &chunkReader{chunks: [][]byte{[]byte(first), []byte(second)}})
}

// assertNoSplitChunks re-runs the script and checks that each chunk handed back
// to the caller does not end in the middle of an escape sequence.
func assertNoSplitChunks(t *testing.T, r *Reader, bufSize int, src *chunkReader) {
	t.Helper()
	buf := make([]byte, bufSize)
	for {
		n, err := r.read(buf, src.Read)
		if n > 0 {
			assert.Equal(t, -1, incompleteTailStart(buf[:n]),
				"a chunk handed to Bubble Tea ended mid-sequence: %q", string(buf[:n]))
		}
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
}

func TestReaderShortReadFlushesLoneEsc(t *testing.T) {
	const esc = "\x1b"
	// A lone Escape keypress is a short read; it must pass through immediately,
	// not be held waiting for a continuation that never comes.
	src := &chunkReader{chunks: [][]byte{[]byte(esc)}}
	r := New(nil)
	got := drainWith(t, r, 256, src)
	assert.Equal(t, esc, string(got))
}

func TestReaderPassesPlainTextUnchanged(t *testing.T) {
	src := &chunkReader{chunks: [][]byte{[]byte("hello world")}}
	r := New(nil)
	got := drainWith(t, r, 256, src)
	assert.Equal(t, "hello world", string(got))
}
