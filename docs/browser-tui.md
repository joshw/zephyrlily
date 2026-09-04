# The TUI in a browser (WebAssembly)

The terminal client also compiles to WebAssembly and runs in a browser tab: the
same Bubble Tea model, the same keymap, the same rendering code, with xterm.js
standing in for the terminal.

This is distinct from the [Svelte web UI](webui.md), which is a separate,
browser-native chat interface. This one *is* the TUI — anything the terminal
client can do, this can do, minus the handful of things a browser tab cannot
(listed at the end).

```
Browser tab
 ├── xterm.js ──── stdin/stdout ────┐
 └── zlily.wasm ───────────────────-┘
        │ fetch + WebSocket, same origin (https → wss)
        ▼
   zlily-proxy  ──TCP──  Lily server
```

## Running it

```sh
# Build the browser client (the native build does not produce it)
GOOS=js GOARCH=wasm go build -ldflags="-s -w" \
  -o internal/webstatic/term/zlily.wasm ./cmd/zlily-wasm

go build ./cmd/zlily
./zlily server --web --lily rpi.lily.org:7777
```

Then open `http://localhost:7888/` — the bare root redirects to the browser
TUI. Log in through the usual auth dialog.

`/term/` still works and is where the assets live; only the root moved. The
Svelte web UI is no longer what `/` gives you; `--web-root=spa` restores that if
you want it.

For access from anywhere but localhost, serve it over TLS — either
`--web-tls --web-cert/--web-key`, or a reverse proxy in front. `--web-tls` on
its own generates a self-signed certificate valid only for localhost.

To put it on the internet properly, `zlily deploy` generates a Docker
deployment behind Traefik with a Let's Encrypt certificate — see
[Deploying](deploying.md), which also covers the rate limiting and session
caps that matter once the URL is public.

`zlily.wasm` is a ~19 MB build artifact and is not committed; the release
pipeline builds it as a `before` hook (`.goreleaser.yaml`), and CI compiles it
on every push so the port cannot rot unnoticed.

## How it fits together

| Piece | What it does |
|---|---|
| `cmd/zlily-wasm` | Entry point. Exposes `zlily.start/write/resize` to JavaScript and runs the same model `runTUI` does. |
| `internal/webstatic/term/term.js` | Owns the xterm.js terminal: feeds keystrokes in, writes rendered bytes out, reports geometry. |
| `internal/tui/onlcr` | Restores the carriage returns a tty driver would normally add (see below). |
| `internal/proxy/api/weburl.go` | Makes outbound web requests (link previews, URL shortening) on the client's behalf. |
| `third_party/bubbletea`, `third_party/clipboard` | Vendored dependencies with the js/wasm platform files upstream lacks. |

### Why the dependencies are vendored

Neither builds for `js/wasm` as published:

- **bubbletea** has `tty_unix.go`/`tty_windows.go` and `signals_unix.go`/
  `signals_windows.go` but no fallback tier, so `initInput`, `suspendProcess`,
  `suspendSupported` and `listenForResize` are undefined for any other GOOS.
  `third_party/bubbletea/{tty,signals}_other.go` supply them, following the
  build-tag shape of the `termios_other.go` that upstream already ships.
- **clipboard** (an indirect dependency via `bubbles/textarea`; nothing in this
  repo calls it) has no js implementation of `readAll`/`writeAll`.

Both are small, self-contained additions and are worth sending upstream; until
then `go.mod` carries `replace` directives.

### The newline translation

The one genuinely surprising detail. Bubble Tea decides whether to map newlines
from whether it holds a tty handle. With no tty — which is every wasm build — it
sets `mapNl` and emits a **bare `\n`** at the end of each rendered line,
expecting the terminal driver's ONLCR to supply the carriage return.

xterm.js does not do ONLCR: it treats `\n` strictly as a line feed. Without
compensation, every frame staircases down and to the right.

`internal/tui/onlcr` restores the carriage returns on the way out. Note the
consequence for the page: **do not set xterm's `convertEol`**, or they get
doubled.

### Never block in a JS callback

The functions exported to JavaScript — `zlily.start`, `write`, `resize` — run on
the goroutine that services JS events. **Anything that waits there waits
forever**: resolving a fetch, firing a timer and delivering the next keystroke
all require returning to the JS event loop first, and that goroutine is what
returns to it. The Go runtime notices and aborts the page with

    fatal error: all goroutines are asleep - deadlock!
    goroutine 1 [select (no cases)]:  main.main()

which names `main`'s `select{}` rather than the call that actually blocked; the
culprit is the goroutine further down the dump, parked in `handleEvent`.

So a callback captures its arguments and returns. `start` reads cols, rows and
the token, creates the input buffer, and hands everything to a goroutine — which
is where the token check's HTTP round trip happens. `prog` is written from that
goroutine and read by the other two callbacks, hence the mutex.

This is easy to reintroduce: any new I/O added to an exported function will look
fine natively and hang only in the browser.

### Caching, and why a rebuild used to need a forced reload

`embed.FS` reports a zero modification time, so `net/http` sends no
`Last-Modified` — and the handler sent no `ETag` or `Cache-Control` either. A
browser handed no validator at all falls back to heuristic caching and may reuse
a stored copy without ever asking again, which for a 20 MB wasm meant a rebuilt
proxy served an old client until someone forced a reload.

It now uses the standard arrangement:

| | Cache-Control | Why |
|---|---|---|
| `index.html` | `no-cache` + `ETag` | Names every other URL, so it must never be stale. Revalidation is a 304 on ~1.6 KB. |
| everything else, with `?v=` | `max-age=31536000, immutable` | The URL carries the build ID, so a new build is a new URL. Nothing to invalidate. |
| anything without `?v=` | `no-cache` | Not safe to pin: nothing would dislodge it. |

The build ID is `webstatic.TermBuildID()`, a hash over every file served under
`/term/` — content, not `version.String()`, because two development builds of
different code share a version string. The server substitutes it for
`__ZLILY_BUILD__` when it serves `index.html`, which is where every asset URL
gets stamped.

So a reload always gets the current client, and a forced reload is never needed.

### Noticing a new build in an open tab

A reload is now always correct, but a tab left open for days keeps running
whatever it loaded. `GET /term/build` returns the current build ID; the page
polls it every five minutes and whenever the tab is refocused, and shows a
"newer zlily is available" banner with a Reload button when it differs.

It offers rather than forces. A reload is cheap now that the session survives
it, but it would still discard a half-typed message, and a mismatch is usually
harmless since the API is the same shape across most builds. Making it
automatic is a one-line change in `showUpdateBanner`.

### Resuming a session across page loads

The proxy session outlives the page. A reload, or a tab the browser discarded
overnight, ends the program while the session it was driving is still live —
which is why logging in again used to land you straight back in the same
session: `handleAuth` hands back the existing token when the password checks
out.

The page now skips that round trip. After every successful login the Go side
offers its token to the host (`zlilySaveToken`), which keeps it in
`localStorage`; the next load passes it back through `zlily.start({token})`,
and `client.ResumeSession` confirms with `GET /session` that it still names a
live session, **then opens the WebSocket**, before the model starts. A token the
proxy does not recognise is discarded and the login dialog appears exactly as
before.

Opening the socket is not optional bookkeeping: `Auth` and `Connect` are
separate steps for the ordinary login too (see `attemptAuthCmd`), and a resumed
client that restored only its token looks authenticated, receives no events, and
dereferences a nil socket on the first command typed — a panic raised inside
`Update`, which loses the session it was trying to preserve. A test that only
exercises HTTP will not notice; `TestE2E_ResumeSessionReattaches` sends a
command and waits for an event for that reason.

Two consequences worth knowing:

- **The token is a bearer credential for a live Lily session.** `localStorage`
  keeps it to this origin and out of every request the browser sends on its own
  — which is what keeps the proxy's bearer scheme immune to CSRF, where a cookie
  would be attached automatically and need its own defence. It is still readable
  by script on this origin, so it is only as safe as this page.
- **A resumed session has no password.** Nothing stores one, so if the Lily
  connection later drops, the client cannot log back in by itself.
  `Client.Reconnect` reports that as an auth failure rather than a generic one,
  so the TUI asks for credentials instead of offering a retry that could never
  succeed.

### Why previews and shortening go through the proxy

In wasm, `net/http` *is* the fetch API, so fetching an arbitrary third-party
site from the page's origin is blocked by CORS for essentially every site worth
previewing. Both features therefore route through the proxy
(`/urlpreview`, `/urlexpand`, `/shorten`), which is also where the s.u13.net
credential stays — a `.wasm` served to a browser is a public download, so
anything compiled into it is readable by anyone who loads the page. The
release build deliberately omits the `u13APIKeyBuild` ldflag for this target.

Native builds still fetch directly (`internal/tui/ui/weburl_other.go`); the
browser build calls the proxy (`weburl_js.go`).

### Origin checking

Serving a browser client means the proxy is reachable off-loopback, so
`handleWS` no longer passes `InsecureSkipVerify`. The default policy accepts a
request whose `Origin` matches the `Host`, and one with no `Origin` at all —
covering the browser build (same origin) and the native TUI (not a browser, so
it sends none) while refusing a socket opened by some other site the user
happens to be visiting.

## Testing it

`internal/integration/browser_*_test.go` runs the real wasm build against a real
proxy and a fake Lily, with Node standing in for the browser. Each test boots
the client through the same bridge the page uses, performs steps, and replays
the captured renderer output through a terminal emulator, so the assertions are
about what a person would see.

    go test ./internal/integration/ -run TestBrowser

They build `cmd/zlily-wasm` once per run and skip themselves when Node is not
installed. The harness is `internal/integration/testdata/browser_harness.cjs`;
it mirrors `term.js` on purpose, and the two drifting apart means these stop
testing what ships.

One wrinkle: Go disables the Fetch API when it recognises Node, routing HTTP
over an in-process fake network that reaches nothing (`jsFetchDisabled` in
`net/http/roundtrip_js.go`, go.dev/issue/57613). The harness presents a
`process` object that does not look like Node so the client takes the same fetch
path a browser would.

These exist because the browser build shares every package with the native
client, so the unit suite passes while the page is broken. Three shipped bugs
prove the point, and each has a test that fails without its fix:

| Bug | Caught by |
|---|---|
| Resume restored the token but not the WebSocket | `TestBrowser_ResumedSessionCanSendCommands` — it sends a command and requires Lily's reply, because anything using HTTP alone passes against a client that cannot send |
| Blocking HTTP on the JS callback goroutine | `TestBrowser_ResumesStoredSession` — the runtime deadlocks and the harness reports the crash |
| Lost newline translation | the same test — appended output must start at column 0, which no substring assertion would notice |

## The splash and the terminal's background

The logo is 256-colour ANSI art of a lily, and it paints its own black
background. On any terminal that is not exactly black — xterm.js defaults to a
dark grey, and most themes are not #000 either — that left the splash sitting in
visible dark patches.

Mapping black to "default background" only fixes half of it. The art also draws
black and near-black *foregrounds*: 36 glyphs in pure black and more just above
it, which is what "partially black characters" looks like. Blanking those was
tried and rejected — the petal outlines are drawn with them, and dropping them
takes the shape apart.

What works is lifting the picture's black point onto the terminal's own
background:

    new = bg + old * (255 - bg) / 255

Black lands exactly on the background, white is untouched, and every gradient in
between is preserved. Measured against `#16161a`, the number of visible colours
darker than the background goes from 130 to 0.

The terminal's colour arrives asynchronously: the client sends an OSC 11 query
(`tea.RequestBackgroundColor`) and redraws the splash when the answer comes
back. In the browser that reply comes from xterm.js's data event, which the page
already pipes into the client — the same path keystrokes take, so nothing extra
was needed. A terminal that never answers keeps the plain black transparency,
and a light background is left alone: lifting a dark picture onto white washes
it out to nothing.

## What does not work in a browser

- **C-z suspend** — there is no job control. C-n and C-t may also be
  unreclaimable from the browser depending on which one you use; C-w and C-r are
  intercepted and do reach zlily.
- **`%debug snapshot`** still works and downloads the file, but the fields that
  shell out — `screen -v`, the `screen -X hardcopy` display dump — and the
  kernel's idea of the tty size are unavailable.
- **The mosh hint** never fires: there is no process table to read, and no mosh
  in the path between the program and its display.
- **Text selection** is the browser's, not the terminal's, so the
  Shift/Option/Fn guidance shown by `%mouse on` does not apply.
- **"Remember password"** in the login dialog cannot work: there is no home
  directory for the credentials file and no OS keyring to reach. Session resume
  above covers the case it was there for. The box is still drawn — gating it
  would mean reworking the dialog's field cycling — so ticking it reports that
  the password could not be saved.
