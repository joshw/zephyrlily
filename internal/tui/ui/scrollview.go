package ui

// scrollView is the output pane's scrolling window over a list of rendered
// lines. It replaces bubbles/viewport for the two panes that hold scrollback
// (output and the debug transcript).
//
// It exists because bubbles/viewport has no way to add content: the only
// entry points are SetContent and SetContentLines, both of which re-ingest
// the whole buffer, and ingestion runs ansi.StringWidth over every line to
// track longestLineWidth. A long session pays that grapheme-cluster scan
// across its entire scrollback on every incoming message — measured at ~23%
// of a 35ms message at the 10,000-item cap, with the rest going on rebuilding
// and joining the line slice that gets thrown straight back into it (see
// docs/responsiveness-findings.md).
//
// None of that work buys this app anything. longestLineWidth exists to serve
// horizontal scrolling and soft wrapping, and zlily uses neither: output is
// pre-wrapped to the viewport width by renderOutputItem, and the panes never
// scroll sideways. Highlights, gutters and per-line style hooks are unused
// too. What is left, once those go, is small enough to own: a line slice, an
// offset, and clamping — with the property the app actually needs, which is
// that appending costs the appended lines rather than the whole scrollback.
//
// Scroll semantics are deliberately identical to bubbles/viewport's, down to
// the clamping and the empty-content case, so the swap is invisible to
// everything built on top (the -- MORE -- pager, resize anchoring,
// click-to-position, snapshot replay). TestScrollViewMatchesBubblesViewport
// holds that parity to the real thing.

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// scrollView holds pre-wrapped lines and the offset of the topmost visible
// one. The zero value is a valid empty view of zero size.
type scrollView struct {
	lines   []string
	width   int
	height  int
	yOffset int
}

// newScrollView returns a view sized to width x height.
func newScrollView(width, height int) scrollView {
	return scrollView{width: width, height: height}
}

// ── geometry ──────────────────────────────────────────────────────────────────

func (s scrollView) Width() int  { return s.width }
func (s scrollView) Height() int { return s.height }

// SetWidth sets the visible width. Lines are pre-wrapped by the caller, so a
// width change means the caller must re-render and re-set the content.
func (s *scrollView) SetWidth(w int) { s.width = w }

// SetHeight sets the visible height, keeping the offset in range: a taller
// view can otherwise sit past the end of its own content.
func (s *scrollView) SetHeight(h int) {
	s.height = h
	s.SetYOffset(s.yOffset)
}

// TotalLineCount is the number of lines held, visible or not.
func (s scrollView) TotalLineCount() int { return len(s.lines) }

// maxYOffset is the largest offset that still fills the view from content.
func (s scrollView) maxYOffset() int {
	return max(0, len(s.lines)-s.height)
}

// ── content ───────────────────────────────────────────────────────────────────

// SetLines replaces the content. The slice is adopted, not copied: callers
// build it for this purpose and must not retain or mutate it afterwards.
func (s *scrollView) SetLines(lines []string) {
	// A single empty line reads as no content at all, matching
	// bubbles/viewport — the difference is visible in TotalLineCount, which
	// the auto-pager anchors on.
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	s.lines = lines
	s.SetYOffset(s.yOffset)
}

// SetContent replaces the content from a newline-separated string. Kept for
// the callers that already hold their content that way (the debug
// transcript); the output pane appends instead.
func (s *scrollView) SetContent(content string) {
	s.SetLines(strings.Split(content, "\n"))
}

// AppendLines adds lines at the end. This is the operation the whole type
// exists for: it costs the lines being added, not the ones already held, and
// it cannot disturb the offset because nothing above the fold moved.
func (s *scrollView) AppendLines(lines []string) {
	if len(lines) == 0 {
		return
	}
	s.lines = append(s.lines, lines...)
}

// TrimTop drops the first n lines and pulls the offset up with them, so the
// view stays on the same content rather than sliding. Used when the scrollback
// cap trims the oldest items.
//
// Re-slicing leaves the dropped lines in the backing array until a later
// append grows it — appending then copies only the live lines, which is what
// keeps repeated trimming amortized rather than a memmove of the whole
// scrollback per trimmed item.
func (s *scrollView) TrimTop(n int) {
	if n <= 0 {
		return
	}
	if n >= len(s.lines) {
		s.lines, s.yOffset = nil, 0
		return
	}
	s.lines = s.lines[n:]
	s.SetYOffset(s.yOffset - n)
}

// ── scrolling ─────────────────────────────────────────────────────────────────

func (s scrollView) YOffset() int { return s.yOffset }

// SetYOffset moves the view, clamped to the content.
func (s *scrollView) SetYOffset(n int) {
	s.yOffset = min(max(n, 0), s.maxYOffset())
}

// AtTop reports whether the first line is visible.
func (s scrollView) AtTop() bool { return s.yOffset <= 0 }

// AtBottom reports whether the view is at (or past) the end of the content.
func (s scrollView) AtBottom() bool { return s.yOffset >= s.maxYOffset() }

func (s *scrollView) GotoTop()    { s.SetYOffset(0) }
func (s *scrollView) GotoBottom() { s.SetYOffset(s.maxYOffset()) }

func (s *scrollView) ScrollUp(n int)   { s.SetYOffset(s.yOffset - n) }
func (s *scrollView) ScrollDown(n int) { s.SetYOffset(s.yOffset + n) }

func (s *scrollView) PageUp()   { s.ScrollUp(s.height) }
func (s *scrollView) PageDown() { s.ScrollDown(s.height) }

// ── rendering ─────────────────────────────────────────────────────────────────

// View renders the visible window, padded to the full width and height so the
// pane occupies its whole area even when the content is shorter.
func (s scrollView) View() string {
	if s.width == 0 || s.height == 0 {
		return ""
	}
	var visible []string
	if len(s.lines) > 0 {
		visible = s.lines[s.yOffset:min(s.yOffset+s.height, len(s.lines))]
	}
	return lipgloss.NewStyle().
		Width(s.width).
		Height(s.height).
		Render(strings.Join(visible, "\n"))
}
