# tview Port Evaluation — Spike Findings

**Question:** Should the zlily TUI be ported from charmbracelet/bubbletea to
rivo/tview?

**Answer: No.** The wins are real but small; the losses sit exactly on
zlily's core surface (scrollback rendering, faithful display of arbitrary
chat text, richly-styled input line). Details and evidence below.

- Evaluated: rivo/tview v0.42.0 on gdamore/tcell v2.13.10
- Date: 2026-07-16, macOS (darwin), Go via `go.mod` toolchain
- Method: a working throwaway prototype (`cmd/zlily-tv` +
  `internal/tui/tv`, ~700 LOC incl. tests) that logged in through the real
  `internal/tui/client` package, rendered live proxy events, and was driven
  headlessly by tests running the real tview event loop on a tcell
  `SimulationScreen`. The spike code was **deleted after evaluation** (it
  was never committed); everything needed to reproduce is embedded in this
  document, including code excerpts in the appendix.

## Why a spike

Advantages tview promised for this codebase: a simpler async bridge
(`QueueUpdateDraw` vs. re-issued `tea.Cmd` listeners), deleting the 153-LOC
`inputguard` stdin workaround, native bracketed paste, built-in
layout/scroll widgets, OSC8 hyperlinks through a supported API, and a
stable API (bubbletea v2 is a breaking rewrite).

Known risks going in: zlily's defining features — OSC8 link grouping across
wrapped lines, a controlled 10k-item scrollback with anchored rewrapping,
per-character-styled input (search highlight, spellcheck underline) — are
hand-rolled against bubbletea's raw-string rendering model, which tview's
cell-grid/tagged-text model does not offer. The spike tested five specific
questions rather than committing to a ~4k-LOC rewrite.

---

## 1. OSC8 hyperlinks under GNU screen

**Mixed: link grouping is better than bubbletea; screen-gating remains our
job; tcell would emit OSC8 under screen if we let it.**

- tcell enables OSC8 emission whenever the terminal's terminfo entry has a
  mouse capability or is xterm-like — `prepareExtendedOSC()` in
  `tscreen.go` (tcell v2.13.10):

  ```go
  if t.ti.Mouse != "" || t.ti.XTermLike {
      t.enterUrl = "\x1b]8;%p2%s;%p1%s\x1b\\"
      t.exitUrl = "\x1b]8;;\x1b\\"
  }
  ```

  tcell's **built-in `screen` terminfo has `Mouse: "\x1b[M"`**, so under
  `TERM=screen` tcell will emit OSC8 — the exact sequences that overflow
  GNU screen 4.00.03's 256-byte escape-string buffer and corrupt the
  display (see `internal/tui/ui/links.go` for the production history).
  There is **no tcell or tview API to disable hyperlink emission**; the
  only lever is never producing a style with a URL. The production
  `TERM=screen*` gate must therefore carry over at the app layer by
  suppressing `[:::url]` tags entirely (appendix, `renderChat`).
- Verified both ways: a unit test confirmed the gate produces zero link
  tags with `osc8Enabled=false`, and a PTY smoke run of the real binary
  under `TERM=screen` (via `script(1)`) contained zero `\x1b]8` bytes.
  The app itself runs fine under `TERM=screen` (tcell ships a compiled-in
  screen terminfo).
- **Wrapped-link grouping is free.** tview assigns each `[:::url]` tag a
  random `UrlId` before wrapping (tview `strings.go`, tag parser:
  `tStyle = tStyle.UrlId(strconv.Itoa(int(rand.Uint32())))`), and tags are
  parsed before line-wrapping, so a URL split across physical lines remains
  one logical link on hover. The manual `linkID` fragment-grouping in
  `internal/tui/ui/links.go` would be deleted in a port, not rewritten.
- The production `maxOSC8URLLen = 2000` cap ports over trivially (skip
  tagging over-long URLs).

## 2. Escaping arbitrary chat text

**Worse than expected: `tview.Escape` is structurally insufficient for a
chat client, not merely tedious.**

With `SetDynamicColors(true)`, every `[...]` span in displayed text is
potential markup, so every render path must escape user/server text
(expected, and confirmed: unescaped `[red]…` renders red). The spike found
two deeper problems:

1. **Escape-then-linkify is exploitable.** The obvious pipeline
   (`linkify(tview.Escape(text))`) breaks on crafted text: the URL regex
   matches *inside* a user's fake tag, and the generated link tag lands
   adjacent to escaped brackets, handing the parser a tag boundary that
   swallows literal text. The fix is single-pass rendering: split the RAW
   text on URL matches and escape each non-URL segment independently
   (appendix, `renderChat`).

2. **Even the correct renderer cannot be made faithful.** tview's tag
   parser (`strings.go` state machine) accepts **any byte in a tag's URL
   field up to the next `]`**. So user text containing an unclosed `[:::`
   merges with the genuine link tag generated after it: the user's `[`
   opens a tag, our tag's bytes complete it, and the literal prefix
   vanishes. Concretely, this chat message:

   ```
   [:::https://evil.example]this must NOT be a link[:::-]
   ```

   renders (after correct segment-wise escaping) as:

   ```
   https://evil.example]this must NOT be a link[:::-]
   ```

   — the leading `[:::` is swallowed. `tview.Escape` cannot help because it
   only rewrites *complete* tag-shaped spans, and here the `[` and its `]`
   sit on opposite sides of the user-text/generated-tag boundary. The
   corruption is bounded (line-local; neighbouring lines, colors, and
   generated links stay intact — verified by test), but faithful rendering
   of adversarial text would require a custom sanitizer with no obvious
   correct construction, or abandoning tagged text for direct cell drawing.

   Escape does handle complete spans correctly: `[red]…[-]`,
   `[[double]]`, `[a[b]c]`, `unterminated [tag and lone ] bracket` all
   rendered literally in tests.

3. One genuine safety improvement: raw ESC/ANSI bytes in chat text cannot
   corrupt the terminal — tcell owns the cell grid, so nothing smuggles
   through to the emulator (unlike bubbletea's raw-string pipeline, where
   sanitization is the app's job).

## 3. Scrollback performance at 10k lines

**Disqualifying for stock `TextView` at production scrollback size.**

Setup: real tview event loop on a `SimulationScreen` at 80×24;
`TextView` with `SetDynamicColors(true)`, `SetWrap(true)`,
`SetWordWrap(true)`, `SetMaxLines(10000)`; 10,000 flood lines of ~180 chars
(every 10th carrying a URL tag), so every line wraps. Timings via
`QueueUpdateDraw` round-trip (it blocks until the update and draw
complete). Numbers stable across repeated runs (±5ms):

| operation                                   | time        |
|---------------------------------------------|-------------|
| append 10k lines + first draw               | ~470–500ms  |
| **every subsequent draw at the tail**       | **~143ms**  |
| draw after resize to 120×40 (full rewrap)   | ~148ms      |
| scroll-to-top + draw                        | ~3ms        |
| draw while scrolled at top                  | ~3ms        |

Interpretation:

- A chat client sits at the tail, and tview redraws on **every keystroke**
  and every arriving message. ~143ms/frame is ~7fps typing latency with a
  full buffer. This is rivo/tview#686 ("TextView consumes significant CPU
  when wrapping is enabled") observed first-hand.
- Cheap draws when scrolled up vs. expensive draws at the tail show the
  cost is end-of-buffer layout indexing over the whole reflowed text, not
  painting. `SetMaxLines` caps memory, not this cost. There is no public
  API to cache or incrementalize the layout.
- `TextView` also has no scroll anchoring across rewraps; production zlily
  preserves the top-visible item through resizes (`scrollAnchor` in
  `ui.go`) and has an auto-pager — neither is expressible on TextView's
  public API.
- Conclusion: a port needs a custom scrollback Primitive with a per-item
  render cache — i.e., a reimplementation of what `ui.go`'s `renderEpoch`
  cache and `format.go` already do, minus the framework benefit.

## 4. Custom input Primitive

**Feasible and pleasant — but it is more custom code, not less.**

Stock `InputField`/`TextArea` expose no per-character rendering hooks, so
spellcheck underlines and search-match highlights cannot ride on them. The
spike implemented a custom `tview.Primitive` (~170 LOC) with:

- per-cell styling: underlined "misspelled" words and reverse-video
  substring highlight, computed as a `[]tcell.Style` per rune (appendix);
- cursor display (`screen.ShowCursor`) with horizontal scroll;
- basic emacs editing (C-a/C-e/C-b/C-f, C-k/C-u, backspace/delete), Enter
  to submit;
- **native bracketed paste** via tview's `PasteHandler` — tcell delivers
  the paste as one string, which would replace the production manual M-p
  paste mode (`pasteEatFlag` machinery in `input.go`) outright, and more
  correctly.

Verdict: tcell's `SetContent`/`ShowCursor` is a comfortable drawing target,
and the production emacs/kill-ring/search logic is mostly pure functions
that would re-home mechanically. But it's a full rewrite of the input
renderer either way.

## 5. Async bridge (WebSocket → UI loop)

**As clean as hoped.** One goroutine draining `client.Events` into
`QueueUpdateDraw` (appendix) replaces bubbletea's `listenCmd` re-issue
pattern and its "must start exactly once" `seenLoopStarted` footgun.
`internal/tui/client` was reused **without changing a line**, as were
`ascify` and the protocol packages. Login (blocking HTTP) ran on a
goroutine posting results back via `QueueUpdateDraw` — also clean.

Caveats:

- One queued draw per message is wasteful in bursts (the Events channel
  buffers 256); a batching drain is trivial but necessary.
- Each `QueueUpdateDraw` pays the full draw cost — with a loaded TextView
  that's 143ms *per message* (§3), so the bridge's cleanliness is
  undermined by the widget it draws.
- Confirmed: tcell's input parser needed no workaround during any of the
  PTY runs — `inputguard` (bubbletea's 256-byte stdin read buffer splitting
  escape sequences) would be deleted in a port.

---

## Recommendation: no-go on a straight port

What tview genuinely offers zlily: the async bridge (§5), deleting
`inputguard` (§5), native bracketed paste (§4), free wrapped-link grouping
(§1), and `Flex`/`Pages`/`Form` for layout, modals, and the login form.

What it costs:

1. **Scrollback**: stock TextView cannot hold 10k wrapped lines at usable
   frame rates (§3). The fix is a custom Primitive reimplementing the
   render cache, scroll anchoring, and pager that `ui.go`/`format.go`
   already implement.
2. **Faithful text display**: tagged-text escaping has an unfixable-in-app
   sharp edge for adversarial chat text (§2). The robust fix is again
   direct cell drawing, bypassing the tag pipeline — i.e., not using
   tview's flagship widget for the app's main surface.
3. **Input line**: needs a custom Primitive regardless (§4).

A port therefore converges on ~4k LOC of custom Primitives drawing directly
on tcell, with tview relegated to layout scaffolding. If bubbletea v1's
limitations ever force a move, the evidence points at **tcell directly**
(or bubbletea v2) rather than tview's widget layer. Durable insight either
way: the transport (`internal/tui/client`) and protocol packages port
untouched, and the hand-rolled string machinery (ANSI-aware wrapping,
render cache, link handling) is the asset to carry forward, not a
liability to shed.

---

## Appendix: key spike code and reproduction notes

Deps used (since removed from `go.mod`): `github.com/rivo/tview v0.42.0`,
`github.com/gdamore/tcell/v2 v2.13.10`.

### Correct single-pass escape + linkify

```go
// osc8Enabled mirrors internal/tui/ui/links.go: GNU screen never forwards
// OSC8 and old builds corrupt on long escape strings; tcell has no disable
// knob, so gate by never emitting URL tags.
var osc8Enabled = !strings.HasPrefix(os.Getenv("TERM"), "screen")

var urlPattern = regexp.MustCompile(`https?://[^\s<>\[\]()]+`)

// renderChat: URLs become [:::url]text[:::-] tags; everything else is
// tview-escaped. MUST be one pass over the raw text — Escape-then-linkify
// is exploitable (see §2). Still not fully faithful for crafted unclosed
// "[:::" prefixes (§2, bounded line-local corruption).
func renderChat(text string) string {
    if !osc8Enabled {
        return tview.Escape(text)
    }
    var b strings.Builder
    last := 0
    for _, loc := range urlPattern.FindAllStringIndex(text, -1) {
        b.WriteString(tview.Escape(text[last:loc[0]]))
        url := text[loc[0]:loc[1]]
        clean := strings.TrimRight(url, ".,;:!?\"'")
        if len(clean) > maxOSC8URLLen {
            b.WriteString(tview.Escape(url))
        } else {
            b.WriteString("[:::" + clean + "]" + tview.Escape(clean) +
                "[:::-]" + tview.Escape(url[len(clean):]))
        }
        last = loc[1]
    }
    b.WriteString(tview.Escape(text[last:]))
    return b.String()
}
```

### Async bridge

```go
// drainEvents replaces the bubbletea listenCmd re-issue pattern.
func (a *App) drainEvents() {
    for msg := range a.client.Events { // client: internal/tui/client, unchanged
        m := msg
        a.app.QueueUpdateDraw(func() { a.appendServerMsg(m) })
    }
    a.app.QueueUpdateDraw(func() { a.setStatus("[red]disconnected[-]") })
}
```

### Per-cell styling in a custom input Primitive (essence)

```go
type Input struct {
    *tview.Box
    text   []rune
    cursor int
}

func (i *Input) Draw(screen tcell.Screen) {
    i.Box.DrawForSubclass(screen, i)
    x, y, w, _ := i.GetInnerRect()
    styles := i.cellStyles() // one tcell.Style per rune:
    //   misspelled words -> style.Underline(true)
    //   search match     -> style.Reverse(true)
    off := 0
    if i.cursor >= w {
        off = i.cursor - w + 1
    }
    for idx, col := off, x; idx < len(i.text) && col < x+w; idx, col = idx+1, col+1 {
        screen.SetContent(col, y, i.text[idx], nil, styles[idx])
    }
    if i.HasFocus() {
        screen.ShowCursor(x+i.cursor-off, y)
    }
}
```

Native paste: implement `PasteHandler() func(string, func(tview.Primitive))`
on the primitive and call `app.EnablePaste(true)`.

### Headless test harness (how the numbers were measured)

```go
sim := tcell.NewSimulationScreen("UTF-8")
_ = sim.Init()
sim.SetSize(80, 24)
a := New("127.0.0.1:1")      // dead proxy; local rendering only
a.app.SetScreen(sim)          // Run() skips Init for a pre-set screen
go func() { _ = a.Run() }()

// QueueUpdate blocks until the event loop executes it (tview
// application.go), so wall-clocking a QueueUpdateDraw round-trip measures
// update + full draw:
timeUpdate := func(name string, f func()) time.Duration {
    start := time.Now()
    a.app.QueueUpdateDraw(f)
    return time.Since(start)
}
timeUpdate("flood+draw", func() { a.flood(10000) }) // ~470–500ms
timeUpdate("draw at tail", func() {})               // ~143ms  <- the problem
sim.SetSize(120, 40)
timeUpdate("draw after rewrap", func() {})          // ~148ms
timeUpdate("scroll top + draw", func() { a.output.ScrollTo(0, 0) }) // ~3ms
```

Flood line shape: `fmt.Sprintf("[yellow]%5d[-] %s", i, renderChat(payload))`
where payload is ~180 chars of prose containing literal `[brackets]` and
`[red]fake tags[-]`, plus `https://example.com/flood/N` on every 10th line.

Screen contents were asserted by flattening `sim.GetContents()` cells to a
string; per-cell styling asserted by comparing `tcell.Style` values
(`Style` is comparable) and via `Style.Decompose()` for colors.

### Adversarial rendering results

| input (chat text)                                        | rendered |
|----------------------------------------------------------|----------|
| `[red]this must NOT render red[-]`                       | literal, default color ✓ |
| `unterminated [tag and lone ] bracket`                   | literal ✓ |
| `nested [[double]] brackets and [a[b]c]`                 | literal ✓ |
| `[:::https://evil.example]this must NOT be a link[:::-]` | **`[:::` swallowed** (§2); remainder literal, damage line-local |
| raw ESC bytes (`\x1b[31m`, raw OSC8)                     | inert; cannot reach the terminal ✓ |

### PTY smoke tests (real binary)

```sh
# normal terminal: form draws, Tab×3 + Enter reaches Quit, exits 0
(sleep 1; printf '\t\t\t\r'; sleep 3) | \
  TERM=xterm-256color script -q typescript ./zlily-tv --proxy 127.0.0.1:1

# under screen: draws fine, and no OSC8 bytes in output
(sleep 1; printf '\t\t\t\r'; sleep 3) | \
  TERM=screen script -q typescript ./zlily-tv --proxy 127.0.0.1:1
grep -c $'\x1b]8' typescript   # -> 0
```

Interactive checks never run (spike deleted first): live login against
Lily, clicking a rendered link in iTerm2, `%flood 10000` inside a real
screen 4.00.03 session. The headless evidence above was judged sufficient
for the no-go call.
