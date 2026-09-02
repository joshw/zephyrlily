#!/bin/sh
#
# verify.sh /path/to/mosh-1.4.0
#
# Runs the two attached tests before and after applying the two patches, and
# reports both outcomes. Everything is asserted rather than assumed: in
# particular the built binaries are checked to be newer than the patched
# sources, because the e2e harness runs src/frontend/mosh-{client,server} and
# a missed rebuild makes a working fix look like a broken one.
#
set -u

TREE="${1:-}"
[ -n "$TREE" ] && [ -d "$TREE/src/terminal" ] || {
    echo "usage: verify.sh /path/to/mosh-1.4.0" >&2; exit 2; }
HERE=$(cd "$(dirname "$0")" && pwd)
TREE=$(cd "$TREE" && pwd)
TESTS="emulation-mode-change-wrap.test emulation-bottom-row-resync.test"

say() { printf '\n=== %s ===\n' "$*"; }

# The tests must be present and registered before anything else.
for t in $TESTS; do
    cp "$HERE"/bug*/"$t" "$TREE/src/tests/$t" || exit 1
    chmod +x "$TREE/src/tests/$t"
done
grep -q emulation-mode-change-wrap "$TREE/src/tests/Makefile.am" || \
    perl -pi -e 's{(emulation-wrap-across-frames\.test)}{$1 \\\n\temulation-mode-change-wrap.test \\\n\temulation-bottom-row-resync.test}' \
        "$TREE/src/tests/Makefile.am"

cd "$TREE" || exit 1
[ -f Makefile ] || { ./autogen.sh && ./configure; } || exit 1

# Assert the binaries the harness will run are newer than every source we
# touch. A stale binary is the one failure mode that silently inverts the
# result of this whole script.
assert_fresh() {
    for src in src/terminal/terminalfunctions.cc src/terminal/terminaldisplay.cc; do
        if [ "$src" -nt src/frontend/mosh-server ]; then
            echo "STALE BUILD: $src is newer than src/frontend/mosh-server." >&2
            echo "The harness runs that binary; the result below would be meaningless." >&2
            exit 1
        fi
    done
}

run_tests() {
    ( cd src/tests && make check TESTS="$TESTS" ) > /tmp/verify-run.log 2>&1
    grep -E "^(PASS|FAIL|ERROR):" /tmp/verify-run.log | sed 's/^/  /'
}

say "building unpatched tree"
make -j4 > /tmp/verify-build.log 2>&1 || { tail -5 /tmp/verify-build.log; exit 1; }
assert_fresh
say "tests BEFORE the fixes (expected: both non-zero)"
run_tests
echo "  --- mosh's own verifier said: ---"
grep -h "round-trip Instruction verification failed" \
     src/tests/emulation-*.test.d/baseline.server.stderr 2>/dev/null | sort -u | sed 's/^/  /'
grep -h "Cursor mismatch" \
     src/tests/emulation-*.test.d/baseline.server.stderr 2>/dev/null | sort -u | sed 's/^/  /'

say "applying both patches"
patch -p1 < "$HERE/bug1-sm-rm-pending-wrap/fix.patch"  || exit 1
patch -p1 < "$HERE/bug2-frame-cursor-desync/fix.patch" || exit 1

say "rebuilding"
make -j4 > /tmp/verify-build2.log 2>&1 || { tail -5 /tmp/verify-build2.log; exit 1; }
assert_fresh
say "tests AFTER the fixes (expected: both PASS)"
run_tests

say "full suite after the fixes"
( make check > /tmp/verify-full.log 2>&1 ) || true
grep -E "^# (TOTAL|PASS|FAIL|ERROR|XFAIL)" /tmp/verify-full.log | sed 's/^/  /'
