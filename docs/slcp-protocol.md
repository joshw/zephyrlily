# SLCP — Simple Lily Client Protocol

This document describes the **SLCP wire protocol** as spoken between the
ZephyrLily proxy and a Lily chat server. It is the protocol on the *server
side* of the proxy; the *client side* of the proxy (the HTTP/WebSocket API the
TUI and other clients consume) is documented separately in
[client-api.md](client-api.md) and [events.md](events.md).

The canonical reference implementation of SLCP is tigerlily (Perl), under
[`reference/tigerlily/`](../reference/tigerlily/) — chiefly
`lib/TLily/Server/SLCP.pm` and the `extensions/slcp_*.pl` files. ZephyrLily's
implementation lives in:

| Concern | File |
|---------|------|
| Line parsing / encoding | [`internal/slcp/parser.go`](../internal/slcp/parser.go), [`internal/slcp/types.go`](../internal/slcp/types.go) |
| Connection, handshake, read loop | [`internal/lily/conn.go`](../internal/lily/conn.go) |
| State model (users, discs, groups, membership) | [`internal/lily/state.go`](../internal/lily/state.go) |
| Fake server used in tests | [`internal/lilytest/fakelily.go`](../internal/lilytest/fakelily.go) |

---

## 1. Transport

- **TCP**, line-oriented, default port **7777**. The proxy can also dial over
  **TLS** (`internal/lily/conn.go` `Connect`).
- Lines are terminated by `\n`; a trailing `\r` is tolerated and stripped on
  read (`readLine`).
- The connection is **full-duplex and asynchronous**: the server pushes
  real-time `%NOTIFY` events at any time, interleaved with the bounded
  responses to commands the client has issued.
- **All text over SLCP is ASCII.** The protocol carries no multi-byte or
  extended encoding; both directions are 7-bit ASCII. ZephyrLily transcodes
  any non-ASCII input the user types down to an ASCII approximation *before*
  sending (`internal/tui/ascify`), so the wire stays within the protocol's
  ASCII contract.

### Line categories

Every received line is one of:

1. A **prompt** that may arrive *without* a trailing newline — `login:` and
   `password:`. `readLine` detects these by a short read timeout followed by a
   prefix match, so the handshake does not block forever waiting for a `\n`
   that never comes.
2. An **SLCP control line**, prefixed with `%` (e.g. `%NOTIFY …`, `%USER …`,
   `%prompt lily>`).
3. A **raw text line** — anything else. Server banners, command output bodies,
   and human-readable status lines (`*** Connected ***`, `Login in the
   wrong.`) are raw text.

Outbound, the client sends two kinds of line:

- **User commands / sends** — a bare line written verbatim (`Conn.Send` appends
  the `\n`). Example: `alice;hello there` or `/who`.
- **Out-of-band client messages**, prefixed with `#$#` (see §7).

---

## 2. Parameter encoding

SLCP control messages after the type token (`%NOTIFY`, `%USER`, `%DISC`,
`%GROUP`, `%DATA`, `%server`) carry a list of parameters. `parseParams`
(`internal/slcp/parser.go`) recognises three forms:

| Form | Meaning |
|------|---------|
| `NAME=VALUE` | Simple string value, read up to the next space. |
| `NAME=LEN=DATA` | **Length-prefixed** value: `DATA` is exactly `LEN` bytes and may contain spaces or newlines. Used for blurbs, titles, and message bodies that contain whitespace. |
| `NAME` | Bare key with no `=` is a **boolean flag**, stored as `"1"`. |

The length-prefixed form is what makes the otherwise space-delimited protocol
able to carry arbitrary text (including embedded `\n`) in a single logical
field. A parser **must** try the `LEN=` interpretation before falling back to a
plain value: a numeric run immediately followed by `=` signals the binary form.

Parameter **keys are upper-case** for event/entity records (`EVENT`, `SOURCE`,
`RECIPS`, `HANDLE`, `NAME`, `BLURB`, …) but **lower-case** for the `%server`
and `%DATA` metadata messages (`version`, `name`, `whoami`). The code reflects
this split.

---

## 3. Login & handshake

The full sequence is driven synchronously by `runHandshake`, then handed off to
the long-lived `readLoop`. Phases are tracked by `ConnPhase`
(`PhaseFirstPrompt` → `PhaseSync` → `PhaseReady`).

```
   Client                                  Server
     │                                        │
     │  ◄──────────  "Welcome to lily at NAME"  (raw banner; server name)
     │  ◄──────────  "login:"                   (no trailing newline)
     │                                        │
     │  ──────────►  #$# options +version +prompt +prompt2 +leaf-notify +leaf-cmd +connected
     │  ──────────►  USERNAME PASSWORD          (space-separated, one line)
     │                                        │
     │  ◄──────────  "*** Connected ***"        (raw; SUCCESS signal, sent early)
     │  ◄──────────  %options +version +prompt …(reply to our #$# options)
     │  ◄──────────  %SLCP-SYNC START
     │  ◄──────────  %USER / %DISC / %GROUP / %DATA …  (entity database snapshot)
     │  ◄──────────  %SLCP-SYNC END
     │  ◄──────────  %connected                 (handshake fully complete)
     │  ◄──────────  %prompt lily>              (server ready for input)
     │                                        │
     │  ──────────►  #$# client zlily VERSION   (announce client)
     │  ──────────►  /where me                  (seed disc membership; response intercepted)
```

### Key facts and pitfalls

- **Credentials may be sent combined** (`USERNAME PASSWORD` on one line, as
  ZephyrLily and tigerlily autologin do) **or separately** in response to the
  `login:` and `password:` prompts. Both are accepted by the server. ZephyrLily
  waits for the actual `login:` prompt before sending, to avoid a premature
  send that the server answers with `Error -2`.

- **`%options` is *not* login confirmation.** It is only the server's reply to
  the client's `#$# options` negotiation and is sent regardless of whether the
  credentials were valid. `validateOptions` checks the advertised options
  include the required set (`+prompt +prompt2 +leaf-notify +leaf-cmd
  +connected`); a missing option is a hard error.

- **Login outcome** is determined by what follows, not by `%options`:
  - **Success:** the server prints the raw line `*** Connected ***` as soon as
    the credentials are accepted. This happens *early* — before the sync block
    and well before `%connected` — so the proxy treats it as the success signal
    and lets `Connect` return promptly.
  - **Failure:** on bad credentials the server prints `Login in the wrong.` and
    **re-prompts** with a (newline-less) `login:`/`password:`. A re-prompt seen
    before login is confirmed is reported as `lily.ErrAuthFailed`, surfaced by
    the proxy as HTTP 401.
  - `%connected` is a guarded **fallback** success signal for servers that omit
    the `*** Connected ***` banner.

- **Do not send user input until options negotiation completes.** Sending too
  early risks `Error -2`.

- The whole dial+login is bounded by `handshakeTimeout` (60s). A watchdog
  closes the socket to break a stalled read, then disarms once login resolves
  so the long-lived `readLoop` is unaffected.

### The SLCP sync block

Between `%SLCP-SYNC START` and `%SLCP-SYNC END` the server streams a snapshot of
its entity database — one record per user, discussion, and group. The proxy
applies these in *sync mode* (`applySync`), which additionally seeds discussion
membership from the `%DISC ATTRIB` field. After `%connected`, records are
applied in *live mode* (`applyLive`). `%connected` itself is **not** forwarded
to clients.

A fresh sync block can be requested at any time after login by sending the
out-of-band line `#$# slcp-sync` (§7). The server re-sends a full
`%SLCP-SYNC START … %SLCP-SYNC END` block, letting a client rebuild its entity
database from scratch without reconnecting. The `%sync` client command is a thin
wrapper that issues exactly this OOB line (handled proxy-side in
`server.go` `dispatchLine`).

> **Implementation note.** The Go parser currently hard-matches the exact
> literals `%SLCP-SYNC beginning` / `%SLCP-SYNC ending`
> (`internal/slcp/parser.go`), so the real `START`/`END` markers fall through to
> `MsgRaw`. This is presently harmless because sync mode is gated on
> `%connected` rather than on the markers, but it should be made tolerant (match
> any `%SLCP-SYNC <token>`, as tigerlily does) before anything relies on the
> delimiters.

### `/where me` interception

Immediately after `%connected`, the proxy sends `#$# client zlily <version>`
and `/where me`, and **silently intercepts** the command response (matched by
its `%begin`/`%end` command id) to populate the set of discussions the user is
a member of (`ApplyWhereResponse`). These lines are suppressed from the client
event stream. The `syncComplete` channel closes once this finishes, unblocking
any caller (e.g. the proxy's `/state` handler) that needs fully-populated
membership.

---

## 4. Message types received

Parsed by `slcp.Parse` into a `slcp.Message` with a `MsgType`
(`internal/slcp/types.go`):

| Wire form | `MsgType` | Meaning |
|-----------|-----------|---------|
| `login:` | `MsgLoginPrompt` | Username prompt (may lack newline). |
| `password:` | `MsgPassPrompt` | Password prompt (may lack newline). |
| `%options …` | `MsgOptions` | Server's reply to `#$# options`. |
| `%SLCP-SYNC START` / `END` | `MsgSyncBegin` / `MsgSyncEnd` | Brackets the entity snapshot. Re-requestable via `#$# slcp-sync`. |
| `%USER HANDLE=… NAME=… …` | `MsgUser` | A user record. |
| `%DISC HANDLE=… NAME=… …` | `MsgDisc` | A discussion record. |
| `%GROUP NAME=… MEMBERS=…` | `MsgGroup` | A named group and its members. |
| `%DATA NAME=… VALUE=…` | `MsgData` | A session metadata datum (`whoami`, `version`, `name`, `events`). |
| `%server version=… name=…` | `MsgServer` | Server metadata. |
| `%NOTIFY EVENT=… …` | `MsgNotify` | A real-time event (see §5). |
| `%begin [42] /cmd` | `MsgCmdBegin` | Start of a command's bounded response. |
| `%end [42]` | `MsgCmdEnd` | End of that response. |
| `%prompt <text>` | `MsgPrompt` | Server is ready for input (`lily>`, secondary prompts). |
| `%connected` | `MsgConnected` | Handshake fully complete. |
| `%pong` | `MsgPong` | Reply to a `#$# ping` keepalive. |
| `%export_file OKAY\|ERROR` | `MsgExportFile` | Result of an `#$# export_file` upload (§7). |
| anything else | `MsgRaw` | Plain text (banners, command output bodies). |

### Entity records

| Record | Fields parsed | Notes |
|--------|---------------|-------|
| `%USER` | `HANDLE`, `NAME`, `BLURB`, `STATE` (`here`/`away`), `PRONOUN` | A logged-in (or known) user. |
| `%DISC` | `HANDLE`, `NAME` (lowercase internal), `TITLE`, `ATTRIB`, `CREATION` (unix ts) | A discussion. `ATTRIB` seeds membership during sync. |
| `%GROUP` | `NAME`, `MEMBERS` (comma-separated handles) | A named convenience group. |

Handles look like `#123`. The state layer (`internal/lily/state.go`) indexes
every entity by **both** handle and lower-cased name, and unifies users, discs,
and groups under one `Entity` type discriminated by `EntityKind`.

### Command response framing

Command output is bracketed by `%begin [id] <label>` and `%end [id]`, where
`id` is a numeric correlation token the client assigns/observes. Each body
line carries its own `%command [id] ` prefix, and that prefix — not the
enclosing bracket — is the routing key: several commands may be in flight at
once with their lines interleaved (tigerlily routes solely by this per-line
tag, never by bracket position). A line with no prefix is ordinary
asynchronous output, not command output. (The proxy uses exactly this framing
to capture the `/where me` response during login, and tolerates untagged body
lines when only a single command is open.)

---

## 5. `%NOTIFY` events

Real-time events arrive as `%NOTIFY EVENT=<type> SOURCE=#id …` and are parsed
into a `slcp.NotifyEvent` by `ParseNotify`. Common fields:

| Field | Meaning |
|-------|---------|
| `EVENT` | Event type (lower-cased on parse). |
| `SOURCE` | Handle of the actor. |
| `TIME` | Unix timestamp. |
| `VALUE` | Body / payload (message text, new name, blurb, reason, …). |
| `RECIPS` | Comma-separated recipient handles (a discussion for public traffic; users for private). |
| `TARGETS` | Comma-separated target handles (permission/appointment ops). |
| `SUBEVT` | Sub-event qualifier (e.g. the privilege named in a `permit`). |
| `EMPTY` | Flag: `VALUE` was explicitly empty (distinguishes "cleared" from "absent"). |
| `NOTIFY` | Flag: this event should be displayed to the user. |
| `STAMP` | Flag: the event's timestamp should be shown. |

> **`NOTIFY` workaround.** The server does not reliably set `NOTIFY=1` for
> message events. `ParseNotify` forces `Notify = true` for `public`, `private`,
> and `emote`, mirroring the "SLCP bug?!" workaround in tigerlily's
> `slcp_parse.pl`.

### Event catalogue

The events ZephyrLily understands (see `eventformat.go` for the full
human-readable formatting and `state.go` `ApplyNotify` for state effects):

**Messages** — `public`, `private`, `emote`, `pa`.

**Presence** — `connect`, `disconnect`, `attach`, `detach`, `here`, `away`,
`unidle`.

**Identity** — `rename` (user renamed), `blurb` (status changed), `info`
(discussion info changed).

**Discussions** — `create`, `destroy`, `join`, `quit`, `retitle`, `drename`
(discussion renamed).

**Permissions / roles** — `permit`, `depermit`, `appoint`, `unappoint`,
`ignore`, `unignore`.

**Misc** — `review`, `sysmsg`, plus others the server may emit (`game`,
`consult`, `sysalert`, …) which pass through as best-effort text.

### Message-event semantics (important)

There are **three** message events, and the distinction matters because clients
use them to drive features like the "reply to last sender" default:

- **`public`** — a send to a **discussion**, seen by everyone there. `RECIPS`
  names the discussion(s).
- **`emote`** — a pose/action (`* Alice waves`). **Emotes are always public** —
  they are sent to a discussion, exactly like `public`. *There is no such thing
  as a private emote.*
- **`private`** — a send addressed **privately to one or more users**. `RECIPS`
  names the recipient users (including yourself when you are a recipient).

A client distinguishing "someone messaged me privately" from "someone spoke in
a room" must key on `private` alone, **not** `emote`. (Tracking `emote` as
private is a real bug class — see `internal/tui/ui/expand.go`
`trackIncomingPrivate` and its caller in `ui.go`.)

### State effects of events

`State.ApplyNotify` mutates the local entity database for a subset of events:

| Event | Effect |
|-------|--------|
| `rename` | Re-key the entity under its new name; update `Name`. |
| `blurb` | Update the user's `Blurb`. |
| `here` / `away` | Update the user's `State`. |
| `disconnect` | Mark the user `away` (record retained). |
| `retitle` | Update the discussion's `Title`. |
| `destroy` | Remove the discussion entities named in `RECIPS` (never the source). |
| `join` / `quit` | When `SOURCE == whoami`, add/remove the `RECIPS` discussions from local membership. |

Other events are forwarded for display but do not change cached state.

---

## 6. `%DATA` session metadata

During sync the server emits `%DATA NAME=<key> VALUE=<val>` for a handful of
session facts (`State.SetData`):

| Key | Meaning |
|-----|---------|
| `whoami` | The current user's own handle (e.g. `#850`). Essential for self-vs-other checks. |
| `version` | Server protocol/software version. |
| `name` | Server name. |
| `events` | Comma-separated list of events this server supports. |

---

## 7. Out-of-band client messages (`#$#`)

Lines prefixed with `#$#` are client-to-server control messages, not user
sends. ZephyrLily uses:

| Message | Purpose |
|---------|---------|
| `#$# options +version +prompt +prompt2 +leaf-notify +leaf-cmd +connected` | Negotiate SLCP options at login. |
| `#$# client zlily <version>` | Announce the client name/version after `%connected`. |
| `#$# slcp-sync` | Request a fresh entity sync. The server re-sends a full `%SLCP-SYNC START … %SLCP-SYNC END` block. Exposed to users as the `%sync` command. |
| `#$# ping` | Keepalive. The server replies `%pong`; the proxy records the time to detect a dead link. |
| `#$# export_file info <len> [target]` | Upload new discussion **info** text (`<len>` lines follow). |
| `#$# export_file memo <bytes> <lines> <name> [target]` | Upload a **memo**. |

For `export_file`, the proxy sends the `#$#` header, streams the content, and
waits for `%export_file OKAY` or `%export_file ERROR` routed back through the
session (`server.go` `handleStore`).

---

## 8. Sending: commands and sends

Outbound user input is written as a raw line (`Conn.Send`). The server
distinguishes a **send** from a **command** by leading punctuation:

- A line with a destination prefix `DEST;message` or `DEST:message` is a
  **send** to the named user(s) or discussion(s). (`;` and `:` are alternate
  separators.)
- A line beginning with `/` is a **slash command** (`/who`, `/join`, `/where`,
  …). Other prefixes (`$`, `?`, `%`, `#`) are likewise commands/control, not
  sends.
- A bare line with no separator is a continuation/normal send to the current
  default destination.

The proxy itself does not parse send syntax beyond what the client API needs;
it forwards the line and lets the server interpret it. Command *responses* come
back framed in `%begin`/`%end` (§4); asynchronous side effects (your own send
being echoed to a room, others seeing it) come back as `%NOTIFY` events.

---

## 9. Relationship to the proxy's client API

The proxy is a **stateful translator**, not a passthrough:

- It maintains the authoritative entity database and membership set from the
  sync block and live events, so HTTP/WebSocket clients can fetch a coherent
  `/state` without replaying the protocol.
- It pre-formats each `%NOTIFY` into a ready-to-display `text` string and
  resolves handles to names, while still forwarding the structured fields. See
  [events.md](events.md) for the per-event field reference on the client side.
- It owns the login lifecycle (including credential retry semantics) and
  surfaces failures as HTTP status codes rather than raw re-prompts.

When extending protocol support, change it at the lowest layer that owns the
concern: parsing in `internal/slcp`, connection/handshake in `internal/lily`,
state in `internal/lily/state.go`, and client-facing shaping in
`internal/proxy/api`.
