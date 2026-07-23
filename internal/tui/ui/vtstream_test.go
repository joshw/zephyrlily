package ui

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/charmbracelet/x/vt"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
)

// miniVT is a minimal terminal emulator that tracks cursor position, DECAWM
// pending-wrap state, and DECSTBM scroll margins. It records any physical
// line wrap (printing past the last column) — autowrap is never intentional
// for a full-screen diff renderer and desyncs it from the real screen,
// producing the vanishing-status-bar symptom. Scrolling via LF at the bottom
// margin is NOT an error: the v2 cursed renderer scrolls deliberately
// (scroll region + newlines is its cheapest way to shift rows).
type miniVT struct {
	width, height int
	row, col      int
	top, bot      int // DECSTBM scroll margins, 0-indexed inclusive
	pendingWrap   bool
	wraps         []string // context for each wrap event
	unknown       []string // CSI finals the emulator doesn't model (would desync tracking)
}

func newMiniVT(width, height int) *miniVT {
	return &miniVT{width: width, height: height, bot: height - 1}
}

func (v *miniVT) printable(r rune, ctx string) {
	if v.pendingWrap {
		v.wraps = append(v.wraps, fmt.Sprintf("wrap at row %d printing %q ctx=%s", v.row, r, ctx))
		v.col = 0
		v.pendingWrap = false
		if v.row != v.bot && v.row < v.height-1 {
			v.row++
		} // at the bottom margin the region scrolls instead; row stays
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
			// Model ONLCR (\n → \r\n): with piped input (as under teatest),
			// bubbletea v2 leaves the output side in cooked mode and its
			// renderer emits bare \n expecting the tty to map it (tea.go:
			// mapNl = ttyInput == nil). A real attached terminal behaves the
			// same way, so column resets here. LF at the bottom margin
			// scrolls the region (deliberate renderer optimization); the
			// cursor stays put. Below a custom region the cursor is pinned.
			v.col = 0
			v.pendingWrap = false
			if v.row != v.bot && v.row < v.height-1 {
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
	case 'E': // CNL: down n, column 0
		v.row += arg(1)
		v.col = 0
		v.pendingWrap = false
	case 'F': // CPL: up n, column 0
		v.row -= arg(1)
		if v.row < 0 {
			v.row = 0
		}
		v.col = 0
		v.pendingWrap = false
	case 'G': // CHA: column absolute
		v.col = arg(1) - 1
		v.pendingWrap = false
	case 'd': // VPA: row absolute
		v.row = arg(1) - 1
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
	case 'r': // DECSTBM: set scroll region (default full screen), cursor homes
		v.top, v.bot = 0, v.height-1
		parts := strings.SplitN(body, ";", 2)
		if len(parts) > 0 && parts[0] != "" {
			if n, err := strconv.Atoi(parts[0]); err == nil && n >= 1 {
				v.top = n - 1
			}
		}
		if len(parts) > 1 && parts[1] != "" {
			if n, err := strconv.Atoi(parts[1]); err == nil && n >= 1 && n <= v.height {
				v.bot = n - 1
			}
		}
		v.row, v.col = 0, 0
		v.pendingWrap = false
	case 'X', 'L', 'M', 'S', 'T', 'P', '@':
		// ECH / IL / DL / SU / SD / DCH / ICH: content moves, cursor doesn't.
		// The v2 cursed renderer (ncurses-style) uses these; the v1 standard
		// renderer never did.
		v.pendingWrap = false
	case 'K', 'J', 'm', 'h', 'l', 'p', 'u', 'W', 'q':
		// erase / SGR / mode set / queries: no cursor movement
	default:
		v.unknown = append(v.unknown, fmt.Sprintf("unhandled CSI final %q in %q", final, seq))
	}
}

func TestLongURLRendererByteStream(t *testing.T) {
	// lipgloss v2 renders colors as specified regardless of environment
	// (profile downgrading moved into the program renderer), so the v1-era
	// global SetColorProfile(TrueColor) pin is no longer needed.
	const width, height = 80, 24

	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false
	// Filler scrollback so the screen is full and scrolling happens.
	for i := 0; i < 40; i++ {
		m.output = append(m.output, OutputItem{Type: "text", Data: fmt.Sprintf("filler line %d", i)})
	}

	// Pin TERM: the renderer picks its control-sequence vocabulary and
	// scroll strategy from the terminal type, so an unpinned environment
	// (e.g. TERM=dumb on CI) produces a different byte stream than the
	// developer machine. xterm-256color exercises the full vocabulary.
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(width, height),
		teatest.WithProgramOptions(
			tea.WithColorProfile(colorprofile.TrueColor),
			tea.WithEnvironment(append(os.Environ(), "TERM=xterm-256color")),
		))

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
		tm.Send(tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 6; i++ {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	time.Sleep(200 * time.Millisecond)

	if err := tm.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}
	out, err := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(5*time.Second)))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	t.Logf("captured %d bytes of renderer output", len(out))

	mvt := newMiniVT(width, height)
	mvt.feed(t, out)
	for _, u := range mvt.unknown {
		t.Errorf("miniVT: %s", u)
	}
	for _, w := range mvt.wraps {
		t.Errorf("physical wrap: %s", w)
	}
}

// TestGrowBoundaryRendererByteStream checks the mirror image of a bug found
// by forensic replay of a real %debug snapshot: backspacing across the
// input area's 2-line->1-line boundary could leave content misplaced onto
// the wrong row, even though renderInputArea()'s own string was proven
// correct by hand. Eventually root-caused (see forceRedraw in ui.go, and
// https://github.com/mobile-shell/mosh/issues/1400) to a false-positive
// scroll-detection heuristic in mosh's Display::new_frame, not a
// bubbletea/ultraviolet bug — but that took ruling out OS, terminal,
// styling, and timing first, and this test was part of that: it drives the
// real renderer (via teatest, not a mock) across the same boundary by typing
// forward instead of backspacing, confirming the corruption is
// shrink-direction only, not symmetric across both directions of the same
// transition.
func TestGrowBoundaryRendererByteStream(t *testing.T) {
	const width, height = 89, 26

	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(width, height),
		teatest.WithProgramOptions(
			tea.WithColorProfile(colorprofile.TrueColor),
			tea.WithEnvironment(append(os.Environ(), "TERM=xterm-256color")),
		))

	time.Sleep(200 * time.Millisecond)

	// Type 88 'a's: n = len+1 = 89 <= firstWidth(89) -> still a single input
	// line, mirroring the post-backspace state that showed the shift bug.
	for i := 0; i < 88; i++ {
		tm.Send(tea.KeyPressMsg{Code: 'a', Text: "a"})
	}
	time.Sleep(100 * time.Millisecond)

	// The 89th 'a': n = 90 > firstWidth(89) -> crosses into a second input
	// line. This is the exact same boundary as the shrink-direction bug,
	// approached from the opposite side.
	tm.Send(tea.KeyPressMsg{Code: 'a', Text: "a"})
	time.Sleep(200 * time.Millisecond)

	if err := tm.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}
	out, err := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(5*time.Second)))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	// tm.Quit()'s shutdown sequence ends with "exit alternate screen buffer"
	// (\x1b[?1049l), which switches the emulator back to the primary buffer
	// this fullscreen app never wrote to — leaving the emulator's view blank
	// if fed the whole stream. Cut it off; we only want the last real frame.
	if i := strings.Index(string(out), "\x1b[?1049l"); i >= 0 {
		out = out[:i]
	}
	t.Logf("captured %d bytes of renderer output (post shutdown-trim)", len(out))

	em := vt.NewEmulator(width, height)
	go func() { _, _ = io.Copy(io.Discard, em) }()
	if _, err := em.Write(out); err != nil {
		t.Fatalf("emulator write: %v", err)
	}

	t.Logf("final screen state after crossing 1-line->2-line by typing forward (%dx%d):", width, height)
	for y := 0; y < height; y++ {
		var sb strings.Builder
		for x := 0; x < width; x++ {
			if c := em.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		trimmed := strings.TrimRight(sb.String(), " ")
		t.Logf("%2d: %q (len=%d, leading-space=%v)", y, trimmed, len(trimmed), strings.HasPrefix(trimmed, " "))
	}
}
