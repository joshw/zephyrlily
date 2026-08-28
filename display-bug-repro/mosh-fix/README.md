# Root cause and fix

## The defect

`src/terminal/terminaldispatcher.cc` clears the pending-wrap flag before running
a dispatch function, when that function is registered as clearing it:

```cpp
  if ( i->second.clears_wrap_state ) {
    fb->ds.next_print_will_wrap = false;
  }
```

and the flag defaults to true (`terminaldispatcher.h`):

```cpp
  Function() : function( NULL ), clears_wrap_state( true ) {}
  Function( Function_Type type, const std::string & dispatch_chars,
            void (*s_function)( Framebuffer *, Dispatcher * ),
            bool s_clears_wrap_state = true );
```

The registrations at the end of `terminalfunctions.cc` are where it goes wrong:

```cpp
  static Function func_CSI_DECSM( CSI, "?h", CSI_DECSM, false );
  static Function func_CSI_DECRM( CSI, "?l", CSI_DECRM, false );
  static Function func_CSI_SGR(   CSI, "m",  CSI_SGR,   false ); /* changing renditions doesn't clear wrap flag */

  static Function func_CSI_SM( CSI, "h", CSI_SM );   /* <- default: clears */
  static Function func_CSI_RM( CSI, "l", CSI_RM );   /* <- default: clears */
```

The DEC private forms and SGR were deliberately marked as not clearing the flag.
The ANSI forms were left at the default, so every `CSI h` / `CSI l` discards a
pending wrap. `CSI_SM` and `CSI_RM` only flip booleans:

```cpp
  static bool *get_ANSI_mode( int param, Framebuffer *fb ) {
    if ( param == 4 ) { /* insert/replace mode */
      return &(fb->ds.insert_mode);
    }
    return NULL;
  }
```

Mode 4 is the only ANSI mode mosh implements, and setting it touches nothing but
`insert_mode`. Neither function moves the cursor, so neither has any business
discarding a pending wrap.

## Verifying it

`wrapprobe.cc` links mosh's real emulator — same Parser, Emulator and
Framebuffer that mosh-server runs — with no network, pty or display. It writes
columns 78-80, emits one sequence, writes one more character, and reports where
that character landed.

```sh
cd mosh-1.4.0
clang++ -std=c++17 -w -I<this dir> -Isrc/util -Isrc/terminal -Isrc/include -Isrc \
  <this dir>/wrapprobe.cc \
  src/terminal/{parser,parseraction,parserstate,terminal,terminaldispatcher,\
terminalframebuffer,terminalfunctions,terminaluserinput}.cc -o wrapprobe
./wrapprobe
```

The bundled `config.h` is a four-line stand-in so the terminal sources build
without running `./configure` (which needs autotools).

Before:

```
  nothing between        row2[1]='X'  OK (wrapped)
  ESC[4l  (RM 4)         row1[80]='X' BUG (overwrote col 80)
  ESC[4h  (SM 4)         row1[80]='X' BUG (overwrote col 80)
  ESC[?25h (DECSET)      row2[1]='X'  OK (wrapped)
  ESC[0m  (SGR)          row2[1]='X'  OK (wrapped)
```

After applying `sm-rm-clears-wrap.patch`, all five wrap correctly.

That table also matches what the black-box tests showed over a real connection,
so the emulator-level defect and the observed corruption line up.

## For a test in mosh's own suite

This needs no network and no state-sync — it is purely a claim about terminal
emulation, so it belongs with the terminal tests rather than the e2e ones. Feed:

```
  ESC[1;78H  "abc"  ESC[4l  "X"
```

to an 80-column `Terminal::Emulator` and assert that row 1 column 80 is still
`c` and that `X` is at row 2 column 1.

## End-to-end verification

Built mosh 1.4.0 twice from the same tree, once with the patch and once without
(protobuf@21 — 29.x produces a client that segfaults on the first resize, which
is a build-environment problem unrelated to this issue). Both pairs then drove a
real `mosh-server`/`mosh-client` loopback over UDP, rendered through tmux, and
were compared against the same bytes rendered directly.

| candidate | unpatched | patched |
|---|---|---|
| `pending-wrap-repro.bin` (30 B) | DIFFER, DIFFER, DIFFER | AGREE, AGREE, AGREE |
| `mosh-repro.bin` (83 B) | DIFFER, DIFFER, DIFFER | AGREE, AGREE, AGREE |

Three runs each, deterministic in both directions. The patch fixes it with real
binaries and a real network path, not only in the emulator.

### What the full capture does not tell us

The original 10,454-byte session capture is **not** a reliable test here, and it
is worth saying why rather than quoting a number from it. Unpatched it differs
consistently, but patched it came back AGREE on one run and DIFFER on the next.

That capture has no `.writes` file, so it is delivered in fixed 40-byte chunks
rather than the renderer's own 84 writes. mosh's state-sync coalesces on write
boundaries, so each run presents a different stimulus and the outcome is not
stable. This is the same gap that made the very first replay attempt fail to
reproduce anything at all.

So the full capture neither confirms nor refutes the fix. The two reduced cases
do, deterministically, and they are what the claim rests on. Re-capturing that
session from a build that records write boundaries would make it usable again.
