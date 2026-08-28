#!/usr/bin/env bash
# deliver.sh <file> [holdsecs] — write a capture to the tty faithfully.
set -u
F="$1"; HOLD="${2:-6}"
saved=$(stty -g 2>/dev/null)
trap 'stty "$saved" 2>/dev/null' EXIT
stty -opost -echo 2>/dev/null
python3 - "$F" <<'PYX'
import os, sys, time
p=sys.argv[1]; d=open(p,'rb').read(); out=sys.stdout.buffer
recs=[]
w=p+'.writes'
if os.path.exists(w):
    for l in open(w):
        l=l.strip()
        if ',' in l:
            a,b=l.split(',',1)
            try: recs.append((int(a),int(b)))
            except ValueError: pass
if recs and sum(n for _,n in recs)==len(d):
    t0=time.monotonic(); off=0
    for ms,n in recs:
        dl=(t0+ms/1000.0)-time.monotonic()
        if dl>0: time.sleep(dl)
        out.write(d[off:off+n]); out.flush(); off+=n
else:
    for i in range(0,len(d),40):
        out.write(d[i:i+40]); out.flush(); time.sleep(0.015)
PYX
sleep "$HOLD"
