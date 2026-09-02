# Two mosh display bugs, each with a failing test and a one-line fix

Both found chasing a single user-visible symptom: the last character of a
terminal application's input line vanishing over mosh (only over mosh —
ssh is clean; iTerm2 and Terminal.app behave identically; GNU screen in the
path is irrelevant; `--predict=never` changes nothing).

| | defect | test (mosh e2e framework) | on stock 1.4.0 |
|---|---|---|---|
| bug 1 | SM/RM clears the pending-wrap flag (emulator) | `emulation-mode-change-wrap.test` | ERROR (exit 99) |
| bug 2 | `new_frame` backspaces from the autowrap column (frame generation / state sync) | `emulation-bottom-row-resync.test` | ERROR (exit 99) |

## Reading the failures: ERROR, not FAIL

On stock 1.4.0 both tests report **ERROR** rather than FAIL. That is not a
broken test — it is mosh detecting the defect with its own machinery.

`transportsender-impl.h` applies every generated diff to a copy of the receiver
state and compares, whenever the server is verbose — which is how the e2e
framework runs it. On these sequences it prints:

```
Cursor mismatch: (23, 79) vs. (23, 78).
Warning, round-trip Instruction verification failed!
```

`e2e-test` greps the server's stderr for that string and calls `test_error`,
which is automake's exit 99 / ERROR, and it does so *before* reaching the
capture comparison. So the verifier short-circuits the FAIL path.

In other words: mosh's own round-trip invariant is violated, and mosh's own
test infrastructure is what says so. With either fix applied, the verifier is
silent and its test passes.

(On macOS the bug 1 test happened to surface as a plain capture mismatch,
exit 1, because frame timing differed and that particular diff was never
generated standalone. On Linux both trip the verifier. Either way the test is
non-zero on stock and zero with the fix; the Linux behaviour is described here
because that is where maintainers and CI run.)

With both one-line fixes applied and both tests added to `src/tests`:
33 tests, 31 PASS, 2 XFAIL, 0 FAIL, 0 ERROR.

To verify from a pristine mosh-1.4.0 tree:

```sh
cp bug*/emulation-*.test mosh-1.4.0/src/tests/     # add both to TESTS in src/tests/Makefile.am
cd mosh-1.4.0 && ./autogen.sh && ./configure && make
# both ERROR (exit 99): "Round-trip Instruction verification failed on server"
( cd src/tests && make check TESTS="emulation-mode-change-wrap.test emulation-bottom-row-resync.test" )

patch -p1 < ../bug1-sm-rm-pending-wrap/fix.patch
patch -p1 < ../bug2-frame-cursor-desync/fix.patch
make

# both PASS, verifier silent
( cd src/tests && make check TESTS="emulation-mode-change-wrap.test emulation-bottom-row-resync.test" )
( cd src/tests && make check )                      # whole suite clean
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
