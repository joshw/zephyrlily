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

## Reduced reproduction (for a mosh test)

`mosh-repro.bin` is 154 bytes, delta-debugged down from the 10,454-byte live
capture. It reproduces through a local stock-mosh loopback:

```
direct: cdefghcdefghijkla                                            AAAB
mosh:   cdefghcdefghijkla                                            AAA
```

Characters are lost off the end of a line, at the right margin. Reduced to its
essentials the stream does this, on an 80-column screen:

```
  ESC[?1049h  ESC[H            enter alt screen, home
  "cdefghcdefghijkla"          17 characters at column 1
  ESC[4l                       IRM off
  ESC[59C  "A"                 jump to column 77, write -> cursor at 78
  ESC[4h " " ESC[4l  BS        insert a space at 78, step back
  ESC[4h "A" ESC[4l  BS        insert 'A' at 78, step back
  ESC[4h "A" ESC[4l            insert 'A'
  "AB"                         write, reaching the right margin
  ESC[4h " " ESC[4l  BS        insert again at the margin
```

The shape is insert-mode (IRM) edits repeated at columns 77-80, i.e. right at
the right margin, where each insert pushes a character off the edge. mosh drops
one more than it should.

Note that plain insertion at a full line, by either ICH or IRM, round-trips
through mosh intact — probed separately and it agrees. So the trigger is
narrower than "insertion at the margin", and the remaining reduction work is to
find which part of the repetition matters.

### Checking a candidate

`moshdiff.sh <file>` renders it directly and through a mosh loopback, both via
`deliver.sh`, and compares with `tmux capture-pane` — the same settled-screen
granularity mosh's own e2e tests use.

```
0  AGREE       screens match
1  DIFFER      mosh's line is a strict prefix of the correct one (this bug)
3  OTHER-DIFF  they differ some other way -- a different fault, not this one
2  harness failure (no CONNECT, blank render); never treated as a result
```

**Controls, which must all pass before any result is believed:**

| candidate | expected |
|---|---|
| plain text, no insert mode | AGREE |
| `ESC[?1049h ESC[H ESC[2J` + `B` | AGREE |
| the full capture | DIFFER |
| `mosh-repro.bin` | DIFFER |

### Two traps this oracle was built around

**Both sides must be delivered identically.** A first version fed the direct
side through `deliver.sh` (raw mode) and the mosh side through a plain `cat`
(cooked mode), so ONLCR rewrote the bare LFs on one side only and every
comparison came out a row off.

**A candidate must establish its own screen state.** With no alt-screen enter,
clear and home, the two renderings start from different cursor positions and
differ for reasons unrelated to the bug. A reducer that was free to delete
those tokens duly "reduced" a real reproduction to a single meaningless byte.
Keep `ESC[?1049h`, `ESC[H` and `ESC[2J` protected.

**And the difference must be the right one.** Accepting any difference lets a
reduction wander into some other mosh fault; the reducer began mutating bytes
inside escape sequences and following whatever broke. Hence the strict prefix
test above.

## Seeing it yourself

`visual-test.sh` shows the bug on screen, by eye, over whatever connection you
run it through. It is self-contained — the 154-byte sequence is embedded — so it
can be copied to any host on its own.

```sh
# resize to exactly 80 columns first (height does not matter)
./visual-test.sh
```

It paints **row 1** using the byte sequence that triggers the bug, and **row 3**
with the same text written plainly. Plain writes are unaffected, so row 3 is
always correct:

- rows 1 and 3 identical → that connection is fine
- row 1 one character shorter → that connection drops characters

A column ruler is printed lower down so the ends can be compared. Verified that
the explanatory text does not itself cause a repaint that would mask the fault:
through a mosh loopback row 1 comes out 79 characters against row 3's 80, and
directly both are 80.

Run it over each path you want to compare — mosh and ssh — and the difference is
visible without any capture, snapshot or tooling.
