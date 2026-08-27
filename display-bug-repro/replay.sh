#!/usr/bin/env bash
# replay.sh [capture.bin] — replay a captured zlily terminal stream faithfully.
#
# "Faithfully" is the whole point, and is where the previous harness went wrong.
# chunkreplay writes to whatever mode the tty happens to be in, which for a
# normal shell is cooked: the driver then rewrites every bare LF the renderer
# emitted into CR+LF (ONLCR). zlily runs in raw mode and emits bare LF meaning
# "cursor down, keep the column", so a cooked replay corrupts the stream before
# any terminal sees it — and then reports display corruption that the
# application never produced. Turning ONLCR off is not a detail; without it the
# replay tests the harness rather than the terminal.
#
# Usage:  ./replay.sh                 # replay the bundled capture
#         ./replay.sh other.bin
#
# Watch the bottom line. It should end up reading, in full:
#   abcdefghijkl abcdefghijkl abcdefghijkl abcdefghijkl abcdefghijkl abcdefghijkl AAABBB
#   (without the spaces — 72 letters then AAABBB, 78 characters)
# Anything shorter, or with gaps, is the bug.
set -u

CAPTURE="${1:-$(dirname "$0")/shrink-capture.bin}"
[ -r "$CAPTURE" ] || { echo "cannot read $CAPTURE" >&2; exit 1; }

# The capture is a recording of an 80x25 session. Replayed at any other size
# every absolute cursor move lands somewhere else, and the result looks broken
# for reasons that have nothing to do with the bug under test.
# stty reads the tty's own window size; tput would need a terminfo entry and a
# TERM that a bare pty may not have.
size=$(stty size 2>/dev/null || echo "0 0")
LINES_=${size% *}
COLS=${size#* }
if [ "$COLS" != "80" ] || [ "$LINES_" != "25" ]; then
  echo "This capture is 80x25; your terminal is ${COLS}x${LINES_}." >&2
  echo "Resize to exactly 80x25 first, or the result means nothing." >&2
  exit 2
fi

saved=$(stty -g)
cleanup() { stty "$saved" 2>/dev/null; printf '\033[?1049l\033[?25h\033[0m\n'; }
trap cleanup EXIT INT TERM

# -opost disables ONLCR and friends; -echo keeps any terminal replies off the
# screen and out of the shell's input.
stty -opost -echo

# Chunked with pauses, the way a real application emits: many small
# render-and-flush cycles rather than one giant write. A single write lets
# state-sync layers coalesce straight to the settled frame and skip the
# intermediate states entirely.
python3 - "$CAPTURE" <<'PY'
import sys, time
data = open(sys.argv[1], 'rb').read()
out = sys.stdout.buffer
for i in range(0, len(data), 40):
    out.write(data[i:i+40]); out.flush()
    time.sleep(0.015)
PY

sleep 20
