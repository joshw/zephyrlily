# ANSI SM/RM (CSI h / CSI l) incorrectly clears the pending-wrap state

## Summary

Setting or resetting an ANSI mode discards a pending wrap. After a character
is written to the last column, the cursor stays there with the wrap owing;
the next printable character should consume the flag and wrap to the next
row. A mode change is not a cursor movement, but `ESC [ 4 h` / `ESC [ 4 l`
between the two makes the next character overwrite the last column instead
of wrapping.

Real-world impact: any application that toggles insert/replace mode while
editing at the right margin (e.g. a line editor built on a renderer that uses
IRM for insertions) renders incorrectly over mosh and correctly over ssh.

## Reproduction (30 bytes, no application needed)

```sh
printf '\033[1;1H%.0s1234567890' 1 2 3 4 5 6 7 8   # fill row 1 (80 cols)
printf '\033[4l'                                    # any SM/RM
printf 'X'                                          # should wrap to row 2
```

Direct/xterm/tmux: row 1 keeps its 80 characters and `X` lands at row 2
column 1. mosh: `X` overwrites row 1 column 80.

`emulation-mode-change-wrap.test` (attached) runs this in the existing e2e
framework. On mosh 1.4.0 it reports **ERROR (exit 99)**, because the resulting
framebuffer state does not survive `Display::new_frame`'s round trip and mosh's
own verifier says so:

```
Cursor mismatch: (23, 79) vs. (23, 78).
Warning, round-trip Instruction verification failed!
```

`e2e-test` promotes that to `test_error` before it reaches the capture
comparison, so the status is ERROR rather than FAIL. The rendered output is
also visibly wrong (`X` on row 1 column 80 instead of row 2 column 1). With the
fix, the verifier is silent and the test passes.

## Cause

`Dispatcher::dispatch` clears `next_print_will_wrap` for any function whose
registration leaves `clears_wrap_state` at its default of true
(`terminaldispatcher.h`). In `terminalfunctions.cc` the DEC private forms and
SGR are already registered as *not* clearing it — SGR carries a comment
saying renditions don't clear the wrap flag — but the ANSI forms were left
at the default:

```cpp
static Function func_CSI_DECSM( CSI, "?h", CSI_DECSM, false );
static Function func_CSI_DECRM( CSI, "?l", CSI_DECRM, false );
static Function func_CSI_SGR(   CSI, "m",  CSI_SGR,   false );

static Function func_CSI_SM( CSI, "h", CSI_SM );   /* <- clears */
static Function func_CSI_RM( CSI, "l", CSI_RM );   /* <- clears */
```

`CSI_SM`/`CSI_RM` only flip mode booleans (mode 4, insert/replace, is the
only ANSI mode implemented) and never move the cursor.

## Fix

`fix.patch` — registers both with `clears_wrap_state = false`, matching the
DEC forms and SGR. With it applied, the attached test passes and the full
`make check` is clean (31 tests, 29 PASS, 2 XFAIL, 0 FAIL, 0 ERROR on the
1.4.0 tree; 33/31 with both attached tests added).
