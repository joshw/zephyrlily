# TUI Responsiveness — Instrumentation and Findings

**Question:** Users report that zlily gets sluggish — less responsive to
typing and scrolling — after it has been running a long time. Is that a memory
leak, and can it be measured and reproduced?

**Answer: it is measurable, reproducible, and now fixed — and it was never a
leak.** Handling one incoming event used to cost time proportional to the
scrollback the session had accumulated: ~1 ms at 100 items, ~35 ms at the
10,000-item cap. Typing itself was always flat at ~0.15 ms. What users felt
was not slow keystrokes but a saturated update loop — a keypress cannot be
handled until the event being processed finishes, and under a busy channel
that wait is most of the perceived delay. Heap tracked the (bounded)
scrollback and flattened with it; nothing grew without limit.

The cost is now flat: 232 µs at the cap against 286 µs at 100 items, and a
sustained 60-second load run holds the update loop at 3% busy where it
previously climbed to 97%.

- Instrumentation: `internal/tui/ui/perf.go`, surfaced by `%debug perf` and in
  every `%debug snapshot`.
- Reproduction: `internal/integration/perfload_test.go` (full stack against
  the fake Lily server), plus `internal/tui/ui/perfscale_test.go` for the
  isolated scaling.
- Fix: `internal/tui/ui/scrollview.go` (a purpose-built replacement for
  bubbles/viewport) and the incremental `syncViewportContent`.

## What was added

### Metrics collected in the TUI

Every `Update` is timed and filed by category — `key`, `scroll`, `paste`,
`event`, `resize`, `other` — along with the two phases those contain or
trigger: `sync` (rebuilding the viewport's content) and `render` (building the
frame string in `View`). Results go into fixed-bucket histograms, so recording
allocates nothing on the keystroke path.

Latencies are bucketed by wall-clock window, because the question is not "how
fast is this session" but "has it got slower". When the window ring fills,
adjacent windows merge pairwise and the window duration doubles: the history
always covers the whole session, at a resolution that halves as it ages, in
bounded memory. Fixed-edge histograms merge exactly, which is what makes that
coarsening lossless for the reported numbers.

Each window also carries a gauge sample — heap, heap objects, goroutines,
scrollback items, rendered lines, bytes written to the terminal — so a latency
trend can be read against whatever is growing underneath it. Sampling calls
`runtime.ReadMemStats`, so it is rate-limited to once per five seconds (or
once per window, whichever is shorter).

### Reading it

`%debug perf` prints the table live; `%debug snapshot` includes the same table
in its new `== responsiveness ==` section. For a slowdown being reproduced
deliberately, `ZLILY_PERF_WINDOW=10s` (read at startup) collects the trend at
a finer resolution than the default one-minute windows.

The **busy** column is the one that connects these numbers to the complaint:
it is the share of wall time the single serialised `Update` loop spent
working. Individual keystrokes can stay fast while a 95%-busy loop still feels
unresponsive, because each press waits its turn behind the event handling
already queued ahead of it. The per-category figures are handling times, not
that wait — the metrics deliberately do not claim to measure queue delay,
which bubbletea's messages carry no arrival timestamp for.

## Reproduction

`TestPerfLoad_SustainedEventStream` drives the real stack — fake Lily → proxy
→ WebSocket → TUI model — with a sustained event stream while typing and
scrolling throughout, then prints the model's own metrics. It ages a session
in seconds instead of days.

```
go test ./internal/integration -run TestPerfLoad -v
ZLILY_LOAD_SECONDS=60 ZLILY_LOAD_RATE=120 ZLILY_LOAD_WINDOW=6s \
    go test ./internal/integration -run TestPerfLoad -v -timeout 300s
```

A 60-second run at 120 events/s, before the fix (abridged; `key`/`event`
cells are `samples/p95`):

```
  elapsed          key      event     busy    heap   objs   items   lines
  0m00s-0m06s 224/500us  702/5.0ms    24%    45.3M   378k     601    1763
  0m12s-0m18s 160/250us   680/10ms    93%    58.1M   440k    1798    5354
  0m30s-0m36s 128/250us   386/25ms    96%    73.0M   534k    3401     10k
  0m54s-1m00s 108/250us   280/25ms    97%    81.1M   587k    4468     13k
  1m00s-1m02s         -    86/50ms    99%    51.5M   588k    4699     14k
```

Per-event p95 rose 5 ms → 50 ms as the scrollback grew 601 → 4,699 items,
and the loop went from 24% to 97% busy. Keystroke handling did not move: p95
stayed at 250 µs throughout, and its *mean* actually improved late in the run.
That is the whole story in one table — the session gets less responsive while
typing itself gets no slower.

Isolating the scaling with no network or scheduler noise, also before the fix
(`go test ./internal/tui/ui -run xxx -bench Scrollback -benchtime 200x`):

```
BenchmarkIncomingMessageByScrollback/items=100      1,040,867 ns/op
BenchmarkIncomingMessageByScrollback/items=1000     4,082,224 ns/op
BenchmarkIncomingMessageByScrollback/items=5000    17,910,733 ns/op
BenchmarkIncomingMessageByScrollback/items=10000   35,208,882 ns/op
BenchmarkKeystrokeByScrollback/items=100              184,091 ns/op
BenchmarkKeystrokeByScrollback/items=10000            152,682 ns/op
```

Linear in scrollback for incoming messages; flat for keystrokes.

## Where the time went

`syncViewportContent` ran on every incoming message and rebuilt the entire
viewport content from scratch: it walked all retained items, concatenated
every rendered line, and handed the joined string to `viewport.SetContent`.
The per-item render cache (`renderItem`) already prevented re-*rendering* the
scrollback, so this was not re-formatting work — it was the re-assembly, and
the viewport's re-ingestion of a string that reaches tens of thousands of
lines. In the trend table `sync` accounted for essentially all of `event`
(11 ms of the 11 ms mean), which is what identified it.

A CPU profile of the benchmark at the cap split it roughly in half: ~37% in
`syncViewportContent` itself (building the 30,000-line slice and joining it),
~23% in `viewport.maxLineWidth` → `ansi.StringWidth` (a grapheme-cluster scan
over every line, run on ingest), and most of the rest in the GC churn those
per-message megabyte allocations produced.

`maxScrollback` caps retention at 10,000 items, so the cost per event plateaued
there rather than growing forever — a session left running for days ended up
pinned at that plateau, roughly 35 ms per event, which matches the "sluggish
after a long time" reports. A busy channel at that plateau saturated the loop.

## Not a memory leak

Heap oscillates between roughly 43 MB and 93 MB across a two-minute run (GC
sawtooth) with no upward drift, live objects track the scrollback and flatten
when it reaches the cap, and the goroutine count stays flat (24, dropping as
connections close). The gauge columns are now in every snapshot, so the same
check can be made on a real session that has been up for days rather than
inferred from a local run.

## Secondary finding: the slow-consumer disconnect

At event rates above what the TUI can consume, the run ends with the proxy
disconnecting the client: `wsClient.enqueue` drops a client that falls
`maxClientBacklog` behind rather than feeding it a stream with invisible gaps,
and the TUI shows its reconnect prompt. That is the designed behaviour, and it
was reached easily before the fix — a 120-second run at 400 events/s hit it at
about the 60-second mark, once per-event cost had risen enough that the client
could no longer keep up. Worth knowing when reading a user report: a busy
channel plus a long-lived session could turn a slowdown into a disconnect. The
harness detects and reports this rather than silently measuring an idle
session afterwards.

## The fix

Two changes, both aimed at the same thing: per-message work must not scale
with what the session has accumulated.

**1. Append instead of rebuild.** `syncViewportContent` now appends only the
lines of items the view has not seen, tracked by `syncedItems`/`syncedEpoch`
on the model. A full rebuild happens only when something invalidates every
line — a width change, the debug split, a whoami change — all of which already
bump `renderEpoch`. The invariant this rests on is that a rendered item never
changes: items are appended at the end and trimmed from the front, and nothing
rewrites the `Data` of one already in the scrollback (`resetSessionIDs` clears
IDs, which are not rendered).

**2. Own the line store.** Appending on our side is not enough while the
content still goes through bubbles/viewport, whose only entry points —
`SetContent` and `SetContentLines` — re-ingest everything and run
`ansi.StringWidth` over every line to track `longestLineWidth`. That accounted
for ~23% of the 35 ms. None of it buys this app anything: `longestLineWidth`
serves horizontal scrolling and soft wrapping, and zlily uses neither (output
is pre-wrapped to the pane width by `renderOutputItem`), nor highlights,
gutters, or per-line style hooks. `scrollView` in
`internal/tui/ui/scrollview.go` is what remains once those go — a line slice,
an offset, and clamping — with `AppendLines` and `TrimTop` costing what they
touch rather than the whole buffer.

Scroll semantics are deliberately identical to the viewport's, and
`TestScrollViewMatchesBubblesViewport` holds that parity against the real
thing across content shorter than, equal to, and longer than the pane: total
line count, offset, `AtBottom`, and the exact rendered frame, after every
operation the app performs. That is what keeps the -- MORE -- pager, resize
anchoring, click-to-position, and snapshot replay behaving as before.

A third change fell out of the second: trimming at the scrollback cap copied
the retained items to a fresh slice on *every* message once the cap was
reached, which is an O(scrollback) memmove back on the hot path. Trimming now
runs in blocks with `scrollbackSlack` hysteresis, the same bulk-eviction
reasoning `dedupCap` already used.

## Results

Isolated scaling (`go test ./internal/tui/ui -run xxx -bench Scrollback
-benchtime 200x`), before and after:

```
items      incoming message           keystroke
           before      after          before     after
100        1,040,867   286,282 ns     184,091    154,402 ns
1000       4,082,224   257,188 ns     154,756    156,776 ns
5000      17,910,733   249,914 ns     148,216    150,063 ns
10000     35,208,882   232,195 ns     152,682    167,061 ns
```

The same 60-second load run at 120 events/s that produced the trend at the top
of this document, after the fix:

```
  elapsed          key      event     busy    heap   objs   items   lines
  0m00s-0m06s 232/500us  702/250us     3%    55.9M   391k     600    1760
  0m12s-0m18s 232/250us  720/500us     3%    64.6M   456k    1802    5366
  0m30s-0m36s 232/500us  721/500us     3%    71.6M   572k    4205     12k
  0m54s-1m00s 232/500us  721/500us     3%    68.3M   665k    6607     19k
```

Every column that used to climb is now flat. Per-event p95 holds at
250–500 µs (was 5 ms rising to 50 ms), lifetime mean is 190 µs (was 11 ms),
`sync` mean is 41 µs (was 11 ms), and the update loop sits at 2.9% busy for
the session (was 85.5%). The run also consumed all 7,200 pushed events and
reached 7,208 scrollback items, where the pre-fix run stalled at 4,699 having
fallen progressively behind the same stream.

`TestIncomingMessageCostDoesNotScaleWithScrollback` guards this: it asserts
per-message cost at the cap stays within 5x of the cost at 500 items (it is
currently ~0.8x, and was ~35x). The assertion is a ratio rather than an
absolute figure so it means the same thing on a slow CI box as on a fast
laptop — what must not come back is the dependence on scrollback size.

## Not pursued

**Coalescing bursts.** Worth noting that the case it would most obviously
help — a reconnect delivering a large detach review at once — was never
affected: the history replay in `initialStateMsg` loops `handleProxy` over
every event and syncs once afterwards. Live-streamed bursts did pay per
message, but at 232 µs each there is no longer a case for batching them.

**Skipping work while scrolled away.** Subsumed: appending below the fold is
already cheap and does not disturb the reader's position.
