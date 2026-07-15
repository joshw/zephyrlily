package ui

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/muesli/termenv"
)

// miniVT is a minimal terminal emulator that tracks cursor position and
// DECAWM pending-wrap state. It records any physical line wrap (printing past
// the last column) and any scroll (LF on the bottom row) — either of which
// desyncs bubbletea's diff renderer from the real screen and produces the
// vanishing-status-bar symptom.
type miniVT struct {
	width, height int
	row, col      int
	pendingWrap   bool
	wraps         []string // context for each wrap event
	scrolls       []string
}

func (v *miniVT) printable(r rune, ctx string) {
	if v.pendingWrap {
		v.wraps = append(v.wraps, fmt.Sprintf("wrap at row %d printing %q ctx=%s", v.row, r, ctx))
		v.row++
		v.col = 0
		v.pendingWrap = false
		if v.row >= v.height {
			v.scrolls = append(v.scrolls, fmt.Sprintf("scroll via wrap ctx=%s", ctx))
			v.row = v.height - 1
		}
	}
	if v.col >= v.width-1 {
		v.col = v.width - 1
		v.pendingWrap = true
	} else {
		v.col++
	}
}

func (v *miniVT) feed(t *testing.T, data []byte) {
	s := string(data)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\r':
			v.col = 0
			v.pendingWrap = false
			i++
		case c == '\n':
			v.pendingWrap = false
			if v.row == v.height-1 {
				v.scrolls = append(v.scrolls, fmt.Sprintf("scroll via LF (col=%d) around byte %d: %q", v.col, i, clip(s, i)))
			} else {
				v.row++
			}
			i++
		case c == 0x08: // BS
			if v.col > 0 {
				v.col--
			}
			v.pendingWrap = false
			i++
		case c == 0x1b:
			n := escSeqLen(s, i)
			if n <= 0 {
				n = 1
			}
			seq := s[i : i+n]
			v.handleSeq(seq)
			i += n
		case c < 0x20:
			i++ // other C0 controls: ignore
		default:
			r, sz := utf8.DecodeRuneInString(s[i:])
			for k := 0; k < ansi.StringWidth(string(r)); k++ {
				v.printable(r, clip(s, i))
			}
			i += sz
		}
	}
}

func clip(s string, i int) string {
	lo, hi := i-40, i+40
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return fmt.Sprintf("%q", s[lo:hi])
}

// handleSeq interprets the cursor-movement CSI sequences bubbletea's standard
// renderer emits. SGR/OSC/private-mode sequences don't move the cursor.
func (v *miniVT) handleSeq(seq string) {
	if len(seq) < 2 || seq[1] != '[' {
		return // OSC or other escapes: no cursor movement
	}
	final := seq[len(seq)-1]
	body := seq[2 : len(seq)-1]
	arg := func(def int) int {
		if body == "" {
			return def
		}
		n, err := strconv.Atoi(body)
		if err != nil {
			return def
		}
		return n
	}
	switch final {
	case 'A':
		v.row -= arg(1)
		if v.row < 0 {
			v.row = 0
		}
		v.pendingWrap = false
	case 'B':
		v.row += arg(1)
		v.pendingWrap = false
	case 'C':
		v.col += arg(1)
		v.pendingWrap = false
	case 'D':
		v.col -= arg(1)
		if v.col < 0 {
			v.col = 0
		}
		v.pendingWrap = false
	case 'H', 'f':
		parts := strings.SplitN(body, ";", 2)
		r, c := 1, 1
		if len(parts) > 0 && parts[0] != "" {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				r = n
			}
		}
		if len(parts) > 1 && parts[1] != "" {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				c = n
			}
		}
		v.row, v.col = r-1, c-1
		v.pendingWrap = false
	case 'K', 'J', 'm', 'h', 'l':
		// erase / SGR / mode set: no cursor movement
	}
}

func TestLongURLRendererByteStream(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	const width, height = 80, 24

	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false
	// Filler scrollback so the screen is full and scrolling happens.
	for i := 0; i < 40; i++ {
		m.output = append(m.output, OutputItem{Type: "text", Data: fmt.Sprintf("filler line %d", i)})
	}

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(width, height))

	// Let the initial frame paint.
	time.Sleep(200 * time.Millisecond)

	// Deliver the long-URL emote like a live proxy event.
	tm.Send(serverEventMsg{msg: &api.WSServerMsg{
		Type: "event",
		ID:   1,
		Data: map[string]interface{}{
			"event":  "emote",
			"source": "#3",
			"value":  " says " + longURL,
			"notify": true,
			"recips": []interface{}{"#5"},
			"entities": map[string]interface{}{
				"#3": map[string]interface{}{"name": "carol"},
				"#5": map[string]interface{}{"name": "cafe"},
			},
		},
	}})
	time.Sleep(200 * time.Millisecond)

	// A few more events so the view scrolls past the URL message.
	for i := 0; i < 30; i++ {
		tm.Send(serverEventMsg{msg: &api.WSServerMsg{
			Type: "text",
			ID:   int64(2 + i),
			Data: map[string]interface{}{"text": fmt.Sprintf("after line %d", i)},
		}})
	}
	time.Sleep(200 * time.Millisecond)

	// Scroll back up over the URL message and down again.
	for i := 0; i < 6; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 6; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	time.Sleep(200 * time.Millisecond)

	tm.Quit()
	out, err := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(5*time.Second)))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	t.Logf("captured %d bytes of renderer output", len(out))

	vt := &miniVT{width: width, height: height}
	vt.feed(t, out)
	for _, w := range vt.wraps {
		t.Errorf("physical wrap: %s", w)
	}
	for _, sc := range vt.scrolls {
		t.Errorf("screen scroll: %s", sc)
	}
}
