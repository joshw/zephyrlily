# Logging the server's own framebuffer

Every probe in this directory replays captured **output**, and the live session
has something they do not — keystrokes going upstream, real latency, real frame
pacing. A capture that reproduces reliably by hand has repeatedly failed to
reproduce on replay, so the modelling has run out of road.

This patch stops modelling and asks the server directly. It logs the bottom row
of `mosh-server`'s own framebuffer after every read from the pty, with the
cursor position and the pending-wrap flag.

That single measurement splits the problem:

- **row correct, screen wrong** -> the loss is downstream of the server's
  emulator, in the state sync or the client;
- **row already wrong** -> the server's emulator is at fault, and the bytes
  zlily sent are all that is needed to chase it.

## Applying

```sh
cd ~/mosh-mosh-1.4.0                     # on top of the two fixes, or alone
patch -p1 < ~/zephyrlily/display-bug-repro/mosh-bug2/server-instrumentation.patch
make -j"$(nproc)"
```

## Running

The variable has to reach `mosh-server`, which `mosh` starts over ssh, so set it
in the server command rather than the local shell:

```sh
mosh --server="MOSH_SERVER_LOG=/tmp/mosh-fb.log $HOME/mosh-mosh-1.4.0/src/frontend/mosh-server" \
     josh@galactic
```

Then reproduce: `%debug redraw off`, type past the input line's wrap point,
backspace back across it until the display goes wrong. Note how many characters
the screen shows when it does.

`/tmp/mosh-fb.log` on the server then holds one line per pty read:

```
read   16 B | cursor r24 c79 wrap=1 | row24 len=79 | AAAABBBB...SSSSTTT
```

The `len` column against what is on your screen is the whole answer. Grab the
last dozen lines and the zlily `%debug snapshot` (M-x) together.

It appends, so clear it between attempts:  `: > /tmp/mosh-fb.log`
