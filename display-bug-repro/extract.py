#!/usr/bin/env python3
"""Extract a replayable capture from a zlily %debug snapshot.

    ./extract.py zlily-debug-YYYYMMDD-HHMMSS.txt [outdir]

Writes <outdir>/capture.bin and <outdir>/capture.bin.writes, ready for
replay.sh. The .writes file is what makes the replay faithful: it restores the
renderer's own write boundaries and pacing, and without it a replay re-slices
the same bytes at different offsets, which is a different stimulus to anything
downstream that syncs per frame.

Also strips the cursor-position query (ESC[6n) from the byte stream. Replaying
that makes the terminal answer, and the reply lands in the shell's input.
"""
import base64
import os
import re
import sys


def section(lines, header):
    for i, line in enumerate(lines):
        if line.startswith(header):
            out = []
            for l in lines[i + 1:]:
                if l.startswith(b'== '):
                    break
                out.append(l)
            return out
    return None


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    snap = sys.argv[1]
    outdir = sys.argv[2] if len(sys.argv) > 2 else os.path.dirname(os.path.abspath(snap))
    os.makedirs(outdir, exist_ok=True)

    lines = open(snap, 'rb').read().split(b'\n')

    tail = section(lines, b'== renderer output tail')
    if tail is None:
        sys.exit("snapshot has no renderer output tail")
    data = base64.b64decode(b''.join(l.strip() for l in tail))

    # Strip the DSR query and adjust the write records that contained it, so
    # the byte count and the pattern still agree.
    query = b'\x1b[6n'

    pattern = section(lines, b'== renderer write pattern')
    recs = []
    if pattern:
        for l in pattern:
            t = l.strip().decode('ascii', 'replace')
            m = re.fullmatch(r'(\d+),(\d+)', t)
            if m:
                recs.append([int(m.group(1)), int(m.group(2))])

    if recs and sum(n for _, n in recs) == len(data):
        # Remove the query from whichever writes contain it, shrinking those
        # records by the same amount.
        off, out, newrecs = 0, bytearray(), []
        for ms, n in recs:
            chunk = data[off:off + n]
            off += n
            cleaned = chunk.replace(query, b'')
            if cleaned:
                newrecs.append((ms, len(cleaned)))
            out += cleaned
        data, recs = bytes(out), newrecs
    else:
        if recs:
            print(f"warning: write pattern ({sum(n for _, n in recs)} bytes) does not match "
                  f"the tail ({len(data)} bytes); dropping it", file=sys.stderr)
            recs = []
        data = data.replace(query, b'')

    cap = os.path.join(outdir, 'capture.bin')
    with open(cap, 'wb') as f:
        f.write(data)
    print(f"wrote {cap} ({len(data)} bytes)")

    if recs:
        with open(cap + '.writes', 'w') as f:
            for ms, n in recs:
                f.write(f"{ms},{n}\n")
        print(f"wrote {cap}.writes ({len(recs)} writes)")
    else:
        print("no usable write pattern in this snapshot -- the replay will fall back to "
              "fixed chunks, which has been observed NOT to reproduce the bug.",
              file=sys.stderr)


if __name__ == '__main__':
    main()
