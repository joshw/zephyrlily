# Second mosh bug: new_frame does not reproduce the pending-wrap state

This is **not** the `SM`/`RM` defect in `../mosh-fix/`. That one is fixed and
verified; this reproduces on a server carrying that patch.

## Symptom

zlily's input line loses its last character over mosh, and only over mosh.

## Where it is not

Ruled out in order, each with a probe:

- **mosh's emulator.** Fed the capture, its framebuffer holds all 78 characters.
- **`new_frame`'s output as rendered by a real terminal.** Emitted the diff for
  each of 6 state transitions and rendered the lot through tmux: correct. Then
  all 21 possible coalescings, painting state *i* and applying the *i→j* diff:
  all 21 correct.
- **The client's rendering, and the local overlays.** Instrumented
  `STMClient::output_new_frame`: the state arriving from the server is already
  short, and `overlays.apply()` leaves it unchanged (77 → 77).

## Where it is

mosh does not ship framebuffers. It ships what `Display::new_frame` emits and
replays that into the client's own `Emulator`
(`statesync/completeterminal.cc`):

```cpp
string update = display.new_frame( true, existing.get_fb(), terminal.get_fb() );
```

So `new_frame` must round-trip: state A, plus the bytes it emits, must give
exactly state B through mosh's own emulator. It does not.

`loopprobe.cc` runs that loop in process, no network:

```
state 5: server 79 (cur 24,79 wrap=1)  client 79 (cur 24,78 wrap=0)  ok
state 6: server 78 (cur 24,79 wrap=0)  client 77 (cur 24,78 wrap=0)  *** DIVERGED ***
```

The divergence begins at **state 5**, where the two agree on every cell and
disagree on the cursor: the server has it on the last column with a wrap
pending, the client one column to the left with none. Nothing notices, because
the check is on cell contents.

It surfaces at state 6, where the diff is 31 bytes of *relative* motion:

```
  ESC[?25l  BS  ESC[0;30;47m " " ESC[0m  ESC[K  ESC[?25h
```

A backspace from column 79 clears the right cell; from 78 it clears one too far
left, and the erase-to-end-of-line then takes the `z` with it.

## The guard that exists, and does not cover this

`terminaldisplay.cc` already knows this hazard, for wrapping rows:

```c
/* If we are about to print the last character in a wrapping row,
   trash the cursor position to force explicit positioning.  We do
   this because our input terminal state may have the cursor on
   the autowrap column ("column 81"), but our output terminal
   states always snap the cursor to the true last column ("column
   80"), and we want to be able to apply the diff to either, for
   verification. */
if ( wrap_this && frame_x + cell_width >= row_width ) {
  frame.cursor_x = frame.cursor_y = -1;
}
```

It is gated on `wrap_this`. An input line on the bottom row does not wrap, so
the guard never fires, and the cursor is left where the emitted bytes happen to
put it rather than where the source state says it is.

## Reproducing

```sh
clang++ -std=c++17 -w -I<config.h dir> -Isrc/util -Isrc/terminal -Isrc/include -Isrc \
  -I/usr/local/opt/ncurses/include loopprobe.cc \
  src/terminal/{parser,parseraction,parserstate,terminal,terminaldispatcher,\
terminaldisplay,terminaldisplayinit,terminalframebuffer,terminalfunctions,\
terminaluserinput}.cc -L/usr/local/opt/ncurses/lib -lncursesw -o loopprobe

TERM=xterm-256color ./loopprobe capture.bin capture.bin.writes
```

No network, no pty, no timing. That makes it a candidate for mosh's own tests:
the invariant is that `new_frame(A,B)` applied to A yields B, cursor and wrap
state included, and it is checkable in a unit test.

## The fix

`append_move` optimises a short leftward step into backspaces:

```c
    // Backspaces are good too.
    if ( y == last_y && x - last_x < 0 && x - last_x > -5 ) {
      append( last_x - x, '\b' );
```

That is only valid from a column the terminal's cursor can actually occupy.
After the last cell of a row is written, `frame.cursor_x` is the autowrap
column -- one past the last -- while the terminal has snapped to the last
column with a wrap pending. A backspace from there starts a cell to the left of
where the bookkeeping thinks and lands a cell early; the next frame's diff then
erases one cell too many, which is the missing character.

`no-backspace-from-autowrap-column.patch` refuses the optimisation in that case
and addresses absolutely instead.

### Two candidates that were wrong

Recorded because both look reasonable and neither survives.

**Dropping `wrap_this` from the guard in `put_row`** (so the cursor is
invalidated before writing the last cell of any row) does not fix it:
`frame.cursor_x` is incremented back to the autowrap column immediately
afterwards, and the final move is still relative.

**Invalidating the cursor after writing the last cell of any row** does fix the
bug, and breaks mosh's own `emulation-80th-column.test`, which exists to pin
"the ancient VT100 behavior of positioning the cursor at column 80 (and not
wrapping) after 80 characters are output". That behaviour is deliberate and
modelled; a fix must leave it alone. It is the reason the fix belongs in
`append_move` rather than in the cursor bookkeeping.

### Verification

`make check` on mosh 1.4.0 with the patch: 31 tests, 29 PASS, 2 XFAIL, **0 FAIL,
0 ERROR** -- including `emulation-80th-column.test`, which the rejected
candidate broke.

End to end over a real mosh-server/mosh-client loopback:

| candidate | SM/RM patch only | both patches |
|---|---|---|
| the 288-byte reduction | DIFFER | **AGREE** |
| the full 9,546-byte session | DIFFER | **AGREE** |
| `../mosh-fix/pending-wrap-repro.bin` | — | AGREE |
| plain-text control | — | AGREE |

And `loopprobe` reports the round trip clean, with the cursor agreeing at state
5 where it previously diverged.
