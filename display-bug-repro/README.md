# Isolating the input-line display corruption

## The symptom

Type past the input line's wrap point, then backspace back across it. One
backspace removes several characters from the display — the screen ends up
showing fewer characters than the app has. It persists until a full repaint.

## What is already known

Measured from a live failure (`%debug snapshot`, taken with M-x so the input
line survived, before any repaint):

| layer | input line | correct? |
|---|---|---|
| zlily's model | 78 chars, ends `AAABBB` | — |
| zlily's emitted bytes | render to 78 chars in tmux | yes |
| **GNU screen's own display model** (hardcopy) | **identical to zlily's frame, 25/25 rows** | **yes** |
| what the user saw (iTerm2 via mosh) | 76 chars, missing `BB` | **no** |

So everything through screen is correct, and the corruption happens below it.
That is the first time it has been localised.

**Screen's model being correct does not exonerate screen.** Screen keeps its
display model and separately generates an output stream for the attached
terminal. A correct model with faulty output generation looks exactly like
this. It stays a suspect.

The renderer edits the input line using insert mode (IRM, `ESC[4h` / `ESC[4l`)
— 30 pairs in the 90 seconds captured here. That is an unusual thing to find in
a byte stream and is the shape of the symptom: one edit displacing several
characters.

## What the first replay attempt showed

Replaying the bytes over the same iTerm2 -> mosh -> screen path that fails
live **did not reproduce it**. The input line came out complete.

That is informative rather than disappointing. The bytes were delivered
byte-for-byte, so the fault does not live in the byte sequence alone: it
depends on something the capture was not preserving. The most likely candidate,
and the one now fixed, is the write boundaries. The renderer emits one write
per frame -- that session was **10K in 84 writes**, ~124 bytes each -- while the
replay sliced at a fixed 40 bytes, turning 84 writes into 261 and splitting
frames apart. Anything downstream that syncs per frame, and mosh's state-sync
protocol most of all, coalesces on exactly those boundaries.

`%debug snapshot` now records the write pattern (`ms,bytes` per write) and
`replay.sh` reproduces it. **The bundled `shrink-capture.bin` predates that and
has no `.writes` file**, so it replays as fixed chunks and is not expected to
reproduce. Take a fresh snapshot to get a faithful one.

## Running it

Capture a fresh failure, then extract and replay:

```sh
# on the machine running zlily: reproduce, then press M-x (not the command --
# it would clear the input line), before resizing or pressing C-l
./extract.py ~/zlily-debug-YYYYMMDD-HHMMSS.txt /tmp/repro
cd /tmp/repro && /path/to/replay.sh capture.bin
```

`extract.py` writes `capture.bin` and `capture.bin.writes`. If it warns that
there is no usable write pattern, the snapshot came from a build before that
was recorded and the replay will not be faithful.



Either way, resize the window to **exactly 80x25** — the capture is a recording
of an 80x25 session, and at any other size every absolute cursor move lands
somewhere else and the result means nothing. The script checks and refuses
otherwise.

Watch the bottom line. It should read 72 letters (`abcdefghijkl` six times)
followed by `AAABBB`, 78 characters in all. Anything shorter, or with gaps, is
the bug.

No zlily, no lily server, and no typing required: this is a recording of a real
failing session's terminal output.

## The matrix, run

| terminal | transport | screen | result |
|---|---|---|---|
| iTerm2 | **mosh** | yes | reproduces |
| iTerm2 | **mosh** | no | reproduces |
| Terminal.app | **mosh** | yes | reproduces |
| Terminal.app | **mosh** | no | reproduces |
| iTerm2 | ssh | yes | clean |
| iTerm2 | ssh | no | clean |

**It is mosh.** Present in every reproduction, absent from every clean run.
The terminal emulator does not matter — iTerm2 and Terminal.app both reproduce.
Screen does not matter either; it drops out in both directions.

That vindicates the original attribution and means the instrumented
re-investigation that cleared mosh was wrong. Its comparisons were made through
`charmbracelet/x/vt`, which does not implement insert mode and so mis-renders
the streams this app produces — it could not have detected a difference it
renders away. See the methodology notes below.

The mechanism inside mosh is still open. Character insertion at a full line, by
either ICH (`ESC[@`) or IRM (`ESC[4h`), survives a mosh loopback intact, so it
is something more specific than insertion alone. Two things worth trying next:
the shrink transition combines a scroll-region change with insertion and a
layout shift, and a faithful replay (one carrying the `.writes` pattern) has not
yet been run through mosh.

## Methodology, learned the hard way

- **Do not use `chunkreplay`** (in `../mosh-scroll-bug-repro/`). It writes to a
  cooked-mode tty, so the driver rewrites every bare LF the renderer emitted
  into CR+LF. zlily runs in raw mode and means those LFs literally. That
  harness corrupts the stream before any terminal sees it, and then reports
  corruption the application never produced. Every reproduction built on it is
  suspect, including the one filed as mobile-shell/mosh#1400. `replay.sh` turns
  ONLCR off and has been verified to deliver the capture byte-for-byte.
- **Do not use `charmbracelet/x/vt` as the reference.** It does not implement
  insert mode:

  ```
  ABCDEF, home, ESC[4h, "xy", ESC[4l
  tmux: xyABCDEF   (correct)
  vt:   xyCDEF     (ignores IRM)
  ```

  zlily's streams are full of IRM, so vt mis-renders them. This matters beyond
  this directory: `../mosh-scroll-bug-repro/investigation/tools/rowdiff.go`
  uses vt as its independent reference, and it is what produced the "mosh
  output matches direct output exactly — 0 differing rows" finding. That
  comparison cannot be relied on, and mosh is not exonerated by it.
- Use **tmux** (`tmux capture-pane`) as the trustworthy emulator. It handles
  IRM correctly and agreed with real terminals in every check made here.
