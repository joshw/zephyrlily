# Display::new_frame emits a backspace from the autowrap column, desyncing client state (caught by mosh's own round-trip verifier)

## Summary

A frame update that ends by writing the bottom-right cell leaves
`FrameState::cursor_x` one past the last column (the "autowrap column"),
while any terminal applying those bytes snaps its cursor to the last column
with a wrap pending. When the target cursor is at the last column,
`FrameState::append_move` renders the one-column difference as a backspace —
issued from the true last column, it lands one cell short. Every subsequent
frame that uses relative motion from there corrupts cells that should have
survived.

Because mosh synchronizes state by replaying `new_frame` output into the
client's own emulator (`statesync/completeterminal.cc`), the corruption is in
the client's *reconstructed state*, persists indefinitely, and is repaired
only by a full repaint.

**mosh's own verification catches this.** With the server in verbose mode
(as the e2e test framework runs it), `transportsender-impl.h` applies each
diff to a copy of the receiver state and compares; on this sequence it prints:

```
Cursor mismatch: (23, 79) vs. (23, 78).
Warning, round-trip Instruction verification failed!
```

Real-world impact: the last character of a full-width editor input line
vanishes when the line is edited back from the right margin — reproducible
interactively over mosh, absent over ssh, on both iTerm2 and Terminal.app.
Confirmed by A/B with only the client binary changed: stock client shows the
corruption, patched client does not (the fix is needed on both ends, since
`new_frame` runs on the server for state sync and on the client for
rendering).

## Reproduction

`emulation-bottom-row-resync.test` (attached) — all cursor addressing in the
test's application bytes is absolute, so the relative motion under test
exists only inside mosh's generated diffs, and the direct rendering is
unambiguous. On mosh 1.4.0 the test exits via the harness's existing
round-trip check ("Round-trip Instruction verification failed on server");
with the fix it passes and the verifier is silent.

`loopprobe.cc` (attached) demonstrates the same invariant violation in
process — no network, no pty: it feeds a byte stream to one `Emulator` at
recorded write boundaries, ships each `new_frame` diff into a second
`Emulator`, and reports the first state where they disagree.

## Cause

`put_row` already knows the hazard for *wrapping* rows — before writing the
last character of a wrapping row it invalidates the cursor "to force explicit
positioning", with a comment explaining that the input state may have the
cursor on the autowrap column while output states snap to the true last
column. But for the bottom row (which does not wrap, and where `put_row`
returns before the CR/LF that re-anchors other rows), the bookkeeping is
left on the autowrap column, and `append_move`'s backspace optimization
computes from it:

```cpp
// Backspaces are good too.
if ( y == last_y && x - last_x < 0 && x - last_x > -5 ) {
  append( last_x - x, '\b' );
```

## Fix

`fix.patch` — `append_move` declines the backspace optimization when
`last_x` is the autowrap column (`last_x < last_frame.ds.get_width()` guard)
and falls through to absolute addressing.

The narrower placement is deliberate: invalidating the cursor after writing
any row's last cell also fixes this, but breaks `emulation-80th-column.test`,
which pins the deliberate VT100 column-80 cursor behavior. The `append_move`
guard leaves that behavior intact: full `make check` is clean with the fix
(and with both attached tests added: 33 tests, 31 PASS, 2 XFAIL, 0 FAIL,
0 ERROR).
