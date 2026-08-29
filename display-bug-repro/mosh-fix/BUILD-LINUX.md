# Building a patched mosh-server on Linux

Two independent defects, two patches. Apply **both**: the first
(`mosh-fix/`) fixes a pending-wrap bug in the emulator, the second
(`mosh-bug2/`) fixes the input-line truncation. Only the second is
responsible for the zlily symptom, but both are real and the first is
cheap to carry.

## Only the server needs it

Tested by mixing binaries across a loopback:

| server | client | result |
|---|---|---|
| stock | stock | broken |
| **patched** | **stock** | **fixed** |
| stock | patched | broken |
| patched | patched | fixed |

`mosh-server` runs the terminal emulator over the application's output; the
client receives framebuffer *states* rather than escape sequences, so the flag
is only ever wrong on the server side. Keep your existing mosh-client.

## Build (Ubuntu/Debian)

```sh
sudo apt update
sudo apt install -y build-essential autoconf automake libtool pkg-config \
                    protobuf-compiler libprotobuf-dev libncurses-dev \
                    libssl-dev zlib1g-dev

cd ~
curl -sSL -o mosh-1.4.0.tar.gz \
  https://github.com/mobile-shell/mosh/archive/refs/tags/mosh-1.4.0.tar.gz
tar xzf mosh-1.4.0.tar.gz
cd mosh-mosh-1.4.0

# both patches; each verified to apply cleanly to a pristine 1.4.0 tree
patch -p1 < ~/zephyrlily/display-bug-repro/mosh-fix/sm-rm-clears-wrap.patch
patch -p1 < ~/zephyrlily/display-bug-repro/mosh-bug2/no-backspace-from-autowrap-column.patch

./autogen.sh
./configure
make -j"$(nproc)"
```

The binary lands at `src/frontend/mosh-server`. There is no need to install it
or to touch the system mosh.

Ubuntu 24.04 ships protobuf 3.21, which is what this was built and verified
against here. Do **not** build against protobuf 29.x: it produces a client that
segfaults on the first window resize, which is a build-environment
incompatibility unrelated to this bug and will waste an afternoon.

## Run against it

`mosh` takes the remote server binary as an argument, so nothing is installed:

```sh
mosh --server=$HOME/mosh-mosh-1.4.0/src/frontend/mosh-server josh@galactic
```

## Confirm you are actually on the patched server

Easy to think you are and not be. Resize to exactly 80 columns and run:

```sh
./visual-test.sh
```

Rows 1 and 3 identical means the patched server is in use. If row 1 is a
character short, the connection is still going through the stock one — check
the `--server=` path.

Then use zlily normally over that connection and try the original trigger: type
past the input line's wrap point and backspace back across it.

If you want a second check that both patches are live, mosh's own suite should
be clean on the patched tree:

```sh
make check          # expect 29 PASS, 2 XFAIL, 0 FAIL, 0 ERROR
```

`emulation-80th-column.test` is the one to watch: a plausible-looking earlier
version of the second patch broke it.
