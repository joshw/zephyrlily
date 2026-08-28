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
