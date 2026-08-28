#!/usr/bin/env bash
# visual-test.sh — show the display bug on screen, by eye, over whatever path
# you are connected through.
#
# Run it over each connection you want to compare (mosh vs ssh, etc). It paints
# one line using the byte sequence that triggers the bug, and directly beneath
# it the same text written plainly. Plain writes are not affected, so the lower
# line is always correct: if the two disagree, this connection has the bug.
#
# Needs an 80-column terminal (the sequence addresses column 77 by a relative
# move, so a different width tests something else). Height does not matter.
set -u

HOLD="${1:-30}"
# The 154-byte reproduction, embedded so this file stands alone: copy it to any
# host and run it over whichever connection you want to test.
REPRO_B64='G1s+NG0bWz0wOzF1G1s/MTA0OWgbWz8yNWwbWz81VxtbPzIwMDRoG1s+NDsybRtbPTE7MXUb
Wz91G1tIY2RlZmdoY2RlZmdoaWprbGEbWzRsG1s1OUNBG1s0aBtbMzA7NDdtIBtbNGwbW20I
G1s0aEEbWzRsCBtbNGhBG1s0bEFCG1s0aBtbMzA7NDdtIBtbNGwbW20IG1s0aA=='

cols=$(stty size 2>/dev/null | awk '{print $2}')
if [ "${cols:-0}" != "80" ]; then
  echo "Needs exactly 80 columns; this terminal has ${cols:-unknown}." >&2
  echo "Resize and re-run, or the test means nothing." >&2
  exit 2
fi

EXPECTED='cdefghcdefghijkla                                                           AAAB'

saved=$(stty -g)
cleanup() {
  # Leave insert mode before anything else: the capture ends with IRM still on,
  # and a shell prompt typed into insert mode is its own kind of confusing.
  printf '\033[4l\033[?25h\033[?1049l\033[0m'
  stty "$saved" 2>/dev/null
}
trap cleanup EXIT INT TERM
stty -echo

printf '\033[?1049h\033[H\033[2J'
printf %s "$REPRO_B64" | base64 -d   # paints row 1 via insert-mode edits at the margin
printf '\033[4l'             # the capture leaves IRM on; stop inserting

printf '\033[3;1H%s' "$EXPECTED"
printf '\033[5;1H%s' 'Row 1 was drawn with insert-mode edits at the right margin.'
printf '\033[6;1H%s' 'Row 3 is the same text written plainly, and is always correct.'
printf '\033[8;1H%s' 'SAME  -> this connection is fine.'
printf '\033[9;1H%s' 'ROW 1 SHORTER -> this connection drops characters: the bug.'
printf '\033[11;1H%s' "(waiting ${HOLD}s; press any key to quit)"
printf '\033[13;1H%s' '         1         2         3         4         5         6         7         8'
printf '\033[14;1H%s' '12345678901234567890123456789012345678901234567890123456789012345678901234567890'

read -r -n 1 -t "$HOLD" _ 2>/dev/null || true
