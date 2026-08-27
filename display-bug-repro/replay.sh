#!/usr/bin/env bash
# replay.sh [capture.bin] — replay a captured zlily terminal stream faithfully.
#
# "Faithfully" is the whole point, and is where the previous harness went wrong
# twice over.
#
# First, the tty mode. chunkreplay writes to whatever mode the tty happens to be
# in, which for a normal shell is cooked: the driver then rewrites every bare LF
# the renderer emitted into CR+LF (ONLCR). zlily runs in raw mode and emits bare
# LF meaning "cursor down, keep the column", so a cooked replay corrupts the
# stream before any terminal sees it, and then reports display corruption the
# application never produced.
#
# Second, the write boundaries. The renderer emits one write per frame, and
# anything downstream that syncs per frame — mosh's state-sync protocol above
# all — coalesces on those boundaries. Re-slicing the same bytes at a fixed
# chunk size delivers every byte faithfully and is still a different stimulus.
# A first attempt chunked at 40 bytes, turning 84 real writes into 261, and did
# not reproduce a fault the live session produced every time.
#
# Usage:  ./replay.sh                 # replay the bundled capture
#         ./replay.sh other.bin
#
# If "<capture>.writes" exists (ms,bytes per line, from a snapshot's "renderer
# write pattern" section) the original writes and pacing are reproduced exactly.
# Without it this falls back to fixed chunks, which has been observed NOT to
# reproduce — so a capture without its .writes file is of limited use.
#
# Watch the bottom line. It should end up reading, in full, 72 letters
# (abcdefghijkl six times) followed by AAABBB — 78 characters. Anything
# shorter, or with gaps, is the bug.
set -u

CAPTURE="${1:-$(dirname "$0")/shrink-capture.bin}"
[ -r "$CAPTURE" ] || { echo "cannot read $CAPTURE" >&2; exit 1; }

# The capture is a recording of an 80x25 session. Replayed at any other size
# every absolute cursor move lands somewhere else, and the result looks broken
# for reasons that have nothing to do with the bug under test.
#
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

python3 - "$CAPTURE" <<'PYX'
import os, sys, time

path = sys.argv[1]
data = open(path, 'rb').read()
out = sys.stdout.buffer

recs = []
wpath = path + '.writes'
if os.path.exists(wpath):
    for line in open(wpath):
        line = line.strip()
        if not line or ',' not in line:
            continue
        ms, n = line.split(',', 1)
        try:
            recs.append((int(ms), int(n)))
        except ValueError:
            pass

if recs and sum(n for _, n in recs) == len(data):
    start = time.monotonic()
    off = 0
    for ms, n in recs:
        delay = (start + ms / 1000.0) - time.monotonic()
        if delay > 0:
            time.sleep(delay)
        out.write(data[off:off + n])
        out.flush()
        off += n
else:
    if recs:
        sys.stderr.write("[write pattern does not match the capture; using fixed chunks]\r\n")
    for i in range(0, len(data), 40):
        out.write(data[i:i + 40])
        out.flush()
        time.sleep(0.015)
PYX

sleep 20
