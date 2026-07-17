# bubbletea v2 Migration — Findings

**Question:** Can zlily move from bubbletea v1.3.10 to bubbletea v2, and is
it advisable?

**Answer: Yes, and yes.** The migration is complete on the `bubbletea-v2`
branch (23 files, +347/−541 — a net *deletion* of ~200 lines), the full test
suite is green, and every kill-shot risk checked out. v2 fixes the input
parser bug that `inputguard` existed for, preserves raw OSC8 passthrough
(with wrapped-link grouping intact), and behaves safely under `TERM=screen`.

- Evaluated: `charm.land/bubbletea/v2` v2.0.8, `charm.land/bubbles/v2`
  v2.1.1, `charm.land/lipgloss/v2` v2.0.5 (the v2 trio went stable
  2026-02-23 — the first breaking release in six years).
- Method: stage 1 was a scratchpad probe program answering the kill-shot
  questions empirically (v1 vs. v2 side-by-side where relevant); stage 2 was
  the actual migration, verified by the test suite plus PTY runs of the real
  binary under `TERM=xterm-256color` and `TERM=screen`.
- Companion doc: `docs/tview-spike-findings.md` (the tview alternative was
  rejected; this migration is the path that evaluation pointed to).

## Stage 1: kill-shot checks (all passed)

### 1. OSC8 hyperlinks survive the new cell-based renderer

v2 replaced the string-diff renderer with the cell-based "Cursed Renderer"
(ultraviolet) — the same architectural family that disqualified tview. The
difference: ultraviolet's `StyledString` parser explicitly reads OSC8
(`ansi.Hyperlink`) sequences from the View string into `Cell.Link{URL,
Params}`, and the renderer re-emits them. `tea.View.Content` documents
hyperlinks as supported content.

Empirical: a probe View containing zlily's exact `osc8Link` byte shape
(`\x1b]8;id=42;https://example.com/probe\x1b\\…`) rendered in a real PTY
re-emits the sequence **with the `id=` param intact** — so the wrapped-link
fragment grouping in `internal/tui/ui/links.go` keeps working unchanged.
(The renderer may re-emit OSC terminators as BEL (`\a`) rather than ST;
both forms are standard.)

### 2. The inputguard bug is fixed — package deleted

The v1 bug (fixed 256-byte stdin buffer splitting escape sequences during
fast wheel bursts, leaking stray keystrokes) reproduced exactly as
`internal/tui/inputguard` documented: feeding v1 a 40-report SGR wheel burst
split mid-sequence at byte 256 yielded **39 mouse events + 2 stray keys**
(`alt+[`, `<65;42;10M`). The identical burst against v2 yielded **40 mouse
events, 0 stray keys** — v2's parser buffers split sequences correctly.
`inputguard` (153 LOC + tests) and its `tea.WithInput` wiring are deleted.

### 3. TERM=screen behavior is safe (with our gate still required)

Under `TERM=screen` v2 emits only short, well-formed CSI sequences at
startup/teardown: DECRQM queries for modes 2026/2027, kitty-keyboard
push/query/pop (`\x1b[=1;1u`, `\x1b[?u`, `\x1b[=0;1u`), modifyOtherKeys
(`\x1b[>4;2m`), bracketed paste, cursor hide/show, DECST8C. These follow
standard CSI grammar, which GNU screen's parser consumes (unanswered
queries are harmless). No DCS, no long OSC strings.

Two carry-overs:

- **The renderer passes OSC8 through regardless of TERM** — it does not
  gate hyperlinks itself. zlily's app-level `osc8Enabled` gate
  (`links.go`) remains necessary and sufficient; PTY runs of the migrated
  binary under `TERM=screen` show zero `\x1b]8` bytes.
- The interactive checklist below still includes a real screen 4.00.03
  session, since the new startup queries are new bytes in that
  environment (predicted harmless — short CSIs — but unverified on the
  real 2003-era binary).

### 4. tmux/COLORTERM handling improved upstream

v2 uses `charmbracelet/colorprofile`, which special-cases tmux by running
`tmux info` and checking Tc/RGB capabilities directly, and refuses
TrueColor under `TERM=screen*`. The `ensureTmuxColor()` workaround in
`cmd/zlily/main.go` is kept (harmless, only sets an unset env var) but is
now mostly redundant — a candidate for later deletion. zlily's 16-color
ANSI palette renders under any profile anyway.

### 5. v1/v2 cannot coexist in one module

`charm.land/bubbletea/v2` forces `charmbracelet/x/ansi` ≥ v0.11.7, which
breaks `x/cellbuf` (a v1 dependency — upstream issue bubbletea#1631). The
migration must be atomic per module; there is no incremental
package-by-package path. (This bit during the probe; the real migration is
atomic anyway.)

## Stage 2: what the migration actually touched

23 files, +347/−541. Nothing framework-agnostic moved: `client`, `ascify`,
`slcp`, `lily`, proxy — zero changes.

Mechanical renames (the bulk):
- Imports → `charm.land/{bubbletea,bubbles,lipgloss}/v2`; teatest →
  `github.com/charmbracelet/x/exp/teatest/v2` (the x repo kept its path).
- `tea.KeyMsg` → `tea.KeyPressMsg`; `msg.Type/Runes/Alt` → `Code`/`Text`/
  `Mod.Contains(tea.ModAlt)`; `" "` key matches → `"space"`.
- Mouse: `tea.MouseMsg`+`MouseButtonWheelUp/Down` →
  `tea.MouseWheelMsg`+`MouseWheelUp/Down`.
- bubbles: viewport `Height`/`YOffset` fields → methods, `viewport.New(w,h)`
  → functional options; textarea `SetCursor` → `SetCursorColumn`.
- lipgloss v2: `Color` is now `func(string) color.Color` (returning
  `ansi.BasicColor`/`ansi.IndexedColor`/`color.RGBA`), `TerminalColor` is
  gone, unset = `NoColor{}` — `style_cmd.go`'s token round-tripping was
  rewritten against those concrete types.

Structural changes:
- `View() string` → `View() tea.View`: the old body became
  `viewContent() string`; `View()` wraps it and **declares** alt-screen,
  cursor (nil — every mode draws its own virtual cursor), and mouse mode.
  `tea.WithAltScreen`, `tea.HideCursor`, `tea.EnableMouseCellMotion`/
  `tea.DisableMouse` all disappeared; `%page wheel` now just flips
  `m.mouseWheel` and `View()` reports the matching `MouseMode`.
- **Paste**: v2 delivers bracketed pastes as `tea.PasteMsg` instead of
  multi-rune KeyMsgs. New `handlePaste` (input.go) routes by mode: auth/
  editor textareas take the message natively; search appends to the
  pattern; paste mode feeds `pasteRune`; normal mode inserts at the
  cursor. Two deliberate behavior improvements: a pasted `;`/`:`/`,`/`=`
  no longer triggers expand (v1 couldn't distinguish paste from typing),
  and stray alt-chords no longer leak their base rune into the input or
  search buffer (v2 gives chords no `Text`).
- Meta-prefix (ESC-then-key) synthesis now sets `Mod |= ModAlt` **and
  clears `Text`** — v2's `Key.String()` prefers `Text`, so a leftover "b"
  would hide "alt+b" from the bindings. (Caught by existing tests.)

Test migration:
- Key literals → `tea.KeyPressMsg{Code: …, Mod: …, Text: …}`.
- The v1-era global `lipgloss.SetColorProfile(TrueColor)` pins are gone;
  determinism now comes from
  `teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.TrueColor))`.
- `vtstream_test.go`'s miniVT terminal emulator learned the cursed
  renderer's wider sequence vocabulary (CHA/VPA/CNL/CPL/ECH/IL/DL/SU/SD/
  DECSTBM — ncurses-style output the v1 renderer never used), it now
  models ONLCR (`\n` → `\r\n`: with piped input v2 leaves the output side
  cooked and relies on the tty mapping — `tea.go` `mapNl`), and it fails
  loudly on any CSI final it doesn't model instead of silently
  mistracking. The renamed-frame assertions (no physical wraps, no
  scrolls) still pass — the GNU-screen-safety property the test guards
  carried over to the new renderer.
- `wheel_test.go` asserts the declarative contract (`m.View().MouseMode`)
  instead of enable/disable commands.

Deleted outright: `internal/tui/inputguard` (153 LOC + tests + wiring),
`muesli/termenv` dependency (its one call site, `termenv.Hyperlink`, became
the same raw OSC8 write the file already used elsewhere).

## Follow-ups (recorded, not done — keep the migration mechanical)

- Replace the manual `inputValue`/`inputCursor` byte tracking: bubbles v2
  textarea finally exposes `Column()`/`SetCursorColumn()`, the missing API
  the hand-rolling worked around (`ui.go` Model comment).
- Consider viewport v2's native soft wrap/highlighting vs. `format.go`'s
  custom wrapping (the custom code also does OSC8-aware layout, so this is
  not a clean swap).
- Consider a real terminal cursor via `View.Cursor` + textarea `Cursor()`
  instead of the drawn `▌`.
- Delete `ensureTmuxColor()` once confirmed redundant in real tmux.
- v2's keyboard enhancements (kitty protocol) could disambiguate chords
  the M-p paste mode exists to paper over; with `PasteMsg` landed, the
  whole manual paste mode may be removable after real-world soak.

## Verification performed

- `go build ./...`, `go vet ./...`, full `go test ./...` green.
- PTY runs of the migrated binary (`script(1)`, 80×24): under
  `TERM=xterm-256color` and `TERM=screen` — auth dialog draws, double-C-c
  quit works, clean exit, zero OSC8 bytes under screen.
- Probe evidence for the renderer/parser claims (§1–2) captured before any
  zlily code changed.

**Remaining for interactive testing** (needs a live terminal / Lily login):
real login, wheel-scroll burst in anger (the original inputguard trigger),
M-p paste + native paste, `%page wheel` toggle, C-z/fg resume repaint,
resize rewrap anchoring, link click in iTerm2, and a session inside real
GNU screen 4.00.03 (watch for artifacts from the new startup queries).

## Advisability

Migrate. The cost was one day of mechanical work (net −200 LOC). The
returns: the input-corruption bug class is gone at the parser level rather
than patched at the read boundary; paste becomes a first-class message;
staying on v1 means pinning to a line that will now only decay (and whose
parser bug upstream will not fix); and the v2 API (declarative View,
Code/Text keys) removes two whole categories of workaround this codebase
had accumulated. No regression was found in the properties zlily actually
depends on — raw OSC8 passthrough, screen 4.00.03 safety, 16-color
rendering, Elm-style testability.
