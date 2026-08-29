# Two mosh display bugs, each with a failing test and a one-line fix

Both found chasing a single user-visible symptom: the last character of a
terminal application's input line vanishing over mosh (only over mosh —
ssh is clean; iTerm2 and Terminal.app behave identically; GNU screen in the
path is irrelevant; `--predict=never` changes nothing).

| | defect | test (mosh e2e framework) | on stock 1.4.0 |
|---|---|---|---|
| bug 1 | SM/RM clears the pending-wrap flag (emulator) | `emulation-mode-change-wrap.test` | FAIL: capture mismatch |
| bug 2 | `new_frame` backspaces from the autowrap column (frame generation / state sync) | `emulation-bottom-row-resync.test` | ERROR: mosh's own round-trip verifier fires |

With both one-line fixes applied and both tests added to `src/tests`:
33 tests, 31 PASS, 2 XFAIL, 0 FAIL, 0 ERROR.

To verify from a pristine mosh-1.4.0 tree:

```sh
cp bug*/emulation-*.test mosh-1.4.0/src/tests/     # add both to TESTS in src/tests/Makefile.am
cd mosh-1.4.0 && ./autogen.sh && ./configure && make
( cd src/tests && make check TESTS="emulation-mode-change-wrap.test emulation-bottom-row-resync.test" )  # both fail
patch -p1 < ../bug1-sm-rm-pending-wrap/fix.patch
patch -p1 < ../bug2-frame-cursor-desync/fix.patch
make && ( cd src/tests && make check )              # clean
```

(Build note: protobuf 3.21 was used here; protobuf 29.x produces a
mosh-client that crashes on resize, unrelated to these bugs.)

Relationship to issue #1400: same user-visible symptom family, but the root
cause proposed there (the scroll-detection heuristic) is not the mechanism of
either bug; these reports supersede that analysis and stand on their own
tests. A third observed divergence (backspace semantics from the pending-wrap
position differ between mosh's emulator and common terminals) is deliberately
not reported: our candidate fix for it regressed a real capture, so it is
documented but withdrawn (see ../withdrawn/ in the source repository).
