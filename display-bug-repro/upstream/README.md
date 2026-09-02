# Two mosh display bugs, each with a failing test and a one-line fix

Both found chasing a single user-visible symptom: the last character of a
terminal application's input line vanishing over mosh (only over mosh —
ssh is clean; iTerm2 and Terminal.app behave identically; GNU screen in the
path is irrelevant; `--predict=never` changes nothing).

| | defect | test (mosh e2e framework) | on stock 1.4.0 |
|---|---|---|---|
| bug 1 | SM/RM clears the pending-wrap flag (emulator) | `emulation-mode-change-wrap.test` | fails |
| bug 2 | `new_frame` backspaces from the autowrap column (frame generation / state sync) | `emulation-bottom-row-resync.test` | fails |

## Verifying

```sh
./verify.sh /path/to/mosh-1.4.0
```

That copies both tests in, registers them, builds, runs them, applies both
patches, rebuilds, and runs them again — printing the before and after. It
asserts that the built binaries are newer than the patched sources at each
step, because the e2e harness runs `src/frontend/mosh-{client,server}` and a
missed rebuild silently turns a working fix into an apparent failure. (That
happened to us; it is the reason the check exists.)

Expected output:

```
=== tests BEFORE the fixes (expected: both non-zero) ===
  ERROR: emulation-mode-change-wrap.test
  ERROR: emulation-bottom-row-resync.test
  --- mosh's own verifier said: ---
  Warning, round-trip Instruction verification failed!
  Cursor mismatch: (23, 79) vs. (23, 78).

=== tests AFTER the fixes (expected: both PASS) ===
  PASS: emulation-mode-change-wrap.test
  PASS: emulation-bottom-row-resync.test

=== full suite after the fixes ===
  # TOTAL: 33   # PASS: 31   # XFAIL: 2   # FAIL: 0   # ERROR: 0
```

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

Which of the two statuses you get is not stable: whether that particular diff
is generated standalone depends on frame timing, so a test may surface as
ERROR (the verifier fired) or as FAIL (a plain direct-vs-mosh capture
mismatch). We have observed both on the same machine across runs. What is
stable is the direction: **non-zero on stock, PASS with the fix.** Judge the
result on that, not on which non-zero status appears.

With both one-line fixes applied and both tests added to `src/tests`:
33 tests, 31 PASS, 2 XFAIL, 0 FAIL, 0 ERROR.

If you would rather drive it by hand than run `verify.sh`, note the one trap:
after `patch`, rebuild from the **top** of the tree and confirm
`src/frontend/mosh-server` is newer than the patched sources before running
`make check`. `make check` inside `src/tests` does not rebuild the frontend,
and the tests will quietly run against the old binaries and still fail.

(Build note: protobuf 3.21 was used here; protobuf 29.x produces a
mosh-client that crashes on resize, unrelated to these bugs.)

Relationship to issue #1400: same user-visible symptom family, but the root
cause proposed there (the scroll-detection heuristic) is not the mechanism of
either bug; these reports supersede that analysis and stand on their own
tests. A third observed divergence (backspace semantics from the pending-wrap
position differ between mosh's emulator and common terminals) is deliberately
not reported: our candidate fix for it regressed a real capture, so it is
documented but withdrawn (see ../withdrawn/ in the source repository).
