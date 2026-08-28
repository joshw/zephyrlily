#!/usr/bin/env bash
# moshdiff.sh <candidate.bin>
# Render the candidate directly, and through a mosh loopback, and diff.
# 0 = AGREE (no bug), 1 = DIFFER (bug reproduced), 2 = harness failure.
#
# Both sides are delivered by the SAME deliverer, in the same tty mode. An
# earlier version fed the direct side through deliver.sh (raw) and the mosh
# side through a plain cat (cooked), so ONLCR rewrote the bare LFs on one side
# only and every comparison was off by a row.
set -u
CAND="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
R=25; C=80
OUT=$(mktemp -d)
cleanup() { rm -rf "$OUT"; tmux kill-session -t md_d 2>/dev/null; tmux kill-session -t md_m 2>/dev/null; }
trap cleanup EXIT

render() { # <file> <session> -> stdout screen
  tmux kill-session -t "$2" 2>/dev/null
  tmux new-session -d -s "$2" -x $C -y $R "/tmp/deliver.sh $1 8" 2>/dev/null
  sleep 6
  tmux capture-pane -p -t "$2" 2>/dev/null | sed 's/[[:space:]]*$//'
  tmux kill-session -t "$2" 2>/dev/null
}

render "$CAND" md_d > "$OUT/direct.txt"

: > "$OUT/server.log"
mosh-server new -p 0 -- /bin/sh -c "sleep 3; /tmp/deliver.sh $CAND 5" > "$OUT/server.log" 2>&1 &
for i in $(seq 1 60); do grep -q "MOSH CONNECT" "$OUT/server.log" 2>/dev/null && break; sleep 0.1; done
LINE=$(grep "MOSH CONNECT" "$OUT/server.log" || true)
[ -z "$LINE" ] && { echo "HARNESS: no CONNECT" >&2; exit 2; }
PORT=$(echo "$LINE" | awk '{print $3}'); KEY=$(echo "$LINE" | awk '{print $4}')
MOSH_KEY="$KEY" TERM=xterm-256color TERMINFO=/usr/share/terminfo \
  python3 /Users/jwilmes/Documents/ZephyrLily/mosh-scroll-bug-repro/investigation/harness/ptyrun.py \
  $R $C "$OUT/mosh.bin" "$OUT/mosh.log" -- mosh-client 127.0.0.1 "$PORT" >/dev/null 2>&1
pkill -f "mosh-server new -p 0" 2>/dev/null || true

[ -s "$OUT/mosh.bin" ] || { echo "HARNESS: empty mosh capture" >&2; exit 2; }
python3 -c "
d=open('$OUT/mosh.bin','rb').read(); i=d.find(b'\x1b[?1049l')
open('$OUT/pre.bin','wb').write(d[:i] if i>=0 else d)"
render "$OUT/pre.bin" md_m > "$OUT/mosh.txt"

# A blank direct render means the harness failed, not that the screens agree.
grep -q . "$OUT/direct.txt" || { echo "HARNESS: blank direct render" >&2; exit 2; }

# DIFFER only for the symptom under investigation: a line whose mosh rendering
# is a strict PREFIX of the direct one, i.e. characters lost off the end. Any
# other difference is a different fault, and accepting it lets a reduction
# wander off to some unrelated mosh bug -- which it promptly did when the
# reducer started mutating bytes inside escape sequences.
python3 - "$OUT/direct.txt" "$OUT/mosh.txt" <<'PYX'
import sys
a=open(sys.argv[1]).read().split("\n")
b=open(sys.argv[2]).read().split("\n")
if a==b:
    print("AGREE"); sys.exit(0)
for i,(x,y) in enumerate(zip(a,b)):
    if x!=y and y and x.startswith(y):
        print("DIFFER"); print(f"row {i}:"); print(f"  direct: {x!r}"); print(f"  mosh  : {y!r}")
        sys.exit(1)
print("OTHER-DIFF")
for i,(x,y) in enumerate(zip(a,b)):
    if x!=y:
        print(f"row {i}: direct={x!r} mosh={y!r}")
        break
sys.exit(3)
PYX
exit $?
