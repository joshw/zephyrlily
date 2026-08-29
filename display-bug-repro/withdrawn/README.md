# A third candidate, withdrawn

While writing a test for bug 2 I found what looked like a third defect and
"fixed" it. It is recorded here because the divergence is real and someone will
find it again — but the fix is wrong, and the way it failed is worth keeping.

## The observation

Fill a row to the last column, then backspace, write a space and erase to end of
line. mosh's own emulator and every real terminal disagree about where that
backspace lands:

```
  ESC[25;1H  <80 digits>  BS  " "  ESC[K

  a real terminal (tmux): row keeps 79 characters
  mosh's emulator:        row keeps 78
```

With the cursor on the last column and a wrap owing, the cursor is logically
past the last column. A real terminal treats the backspace as cancelling the
wrap and staying put; mosh steps left as well, so it moves two cells' worth.

`Ctrl_BS` is registered with the default `clears_wrap_state`, so the dispatcher
discards the flag before the handler can see it — the same shape as bug 1.

## Why the fix is withdrawn

Making backspace cancel the wrap instead of stepping left does make that case
match a real terminal, and `emulation-last-column-backspace.test` passes with
it. But it **regresses the real reproduction**: with it applied, the captured
zlily session renders one character too *many*.

```
  direct:  ...uvwxyz
  mosh:    ...uvwxyzA
```

So the observation stands and the remedy does not. Either the correct behaviour
is more subtle than "cancel the wrap", or something downstream compensates for
the current behaviour and both would have to move together.

It is withdrawn rather than filed because a patch that fixes a synthetic case
and breaks a real one is exactly the kind of report that wastes a maintainer's
afternoon. The test is kept as documentation of the divergence, not as a
failing test to act on.
