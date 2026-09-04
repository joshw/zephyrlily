# Authentication and Login Flow

This document describes the end-to-end login process for ZephyrLily, covering how the TUI client authenticates with the proxy and how the proxy authenticates with the Lily server.

## Overview

```
TUI client  ──GET /info───▶  zlily-proxy  ──TCP──▶  Lily server
 (+ saved    ◀─ lily addr ─   (localhost)
  creds)     ──POST /auth──▶
            ◀── token ───
            ──Bearer token──▶  (all subsequent requests)
```

The proxy runs as a localhost-only process. The TUI communicates with it over HTTP and WebSocket; the proxy maintains a persistent TCP connection to the Lily server on the TUI's behalf.

---

## Step 0 — Where the credentials come from

The dialog is usually not typed into twice. While it is being drawn, the TUI calls `GET /info` to learn which Lily server the proxy fronts, then looks up that server in two client-side stores (`internal/tui/creds`):

1. `~/.config/zlily/credentials` — tab-separated, one account per line, mode 0600. Read **first**, so a line the user added by hand overrides everything else. A file that others can read is ignored, with a line in the dialog saying so.
2. The OS keyring — macOS Keychain, Windows Credential Manager, or a freedesktop Secret Service, under the service name `zlily:<lily addr>`. Every keyring call is bounded by a 3-second timeout: zlily's usual home is a headless host with no Secret Service running, and a missing keyring must cost nothing.

Saving inverts the order — the keyring when the machine has one, the file when it does not — and a save that reaches the keyring deletes any file entry for the same account, since the file is read first and a stale line there would shadow it forever.

The username alone is not a secret and is remembered on every successful login (`~/.config/zlily/config.json`), which is why the dialog opens with the cursor on the password field. A password is only ever written when the user ticks "Remember password" or runs `%save-password`; clearing that box on a later login removes what was stored, as does `%forget-password`.

Two rules keep a stored password from becoming a nuisance:

- A stored password Lily **rejects** is deleted, not offered again at the next launch. The dialog says so, so the vanished prefill is not a mystery.
- Keys typed into the dialog are recorded in the input-event ring **without their text** (`recordKeyEvent`), so a `%debug snapshot` attached to a bug report does not carry the password a character at a time.

All of this is client-side on purpose. The proxy is multi-user, and in `zlily client` mode the credential belongs to the person at the terminal, not to the machine holding the session.

---

## Step 1 — TUI calls `POST /auth`

The TUI collects credentials in a modal dialog and `attemptAuthCmd`
(`internal/tui/ui/ui.go`) calls `client.Auth(username, password)`, which POSTs:

```json
{ "username": "josh", "password": "..." }
```

to the proxy's `/auth` endpoint. The password is never stored or logged by the proxy.

---

## Step 2 — Proxy checks for an existing session

`handleAuth` first looks up `username` in `userTokens` (an in-memory `username → token` map). If a live session already exists for that username, the supplied password is checked against that session's `authVerifier` (a salted SHA-256 of the password that opened it, compared with `subtle.ConstantTimeCompare`). On a match, the existing token is returned immediately — no new Lily connection is opened and no new token is minted. This handles clients that call `/auth` again after a brief disconnect without tearing down the Lily TCP connection.

On a mismatch the proxy returns HTTP 401 and **leaves the live session alone**: tearing it down here would let anyone who knows a username knock that user offline. The check cannot be delegated to Lily, either — a second login for the same handle does not fail, it redirects the session to the new connection (`*** Redirecting old connection to this port ***`), so verifying that way would detach the session being verified.

---

## Step 3 — Proxy connects to the Lily server

If there is no live session, `handleAuth` creates a `lily.Conn` and calls `conn.Connect()`. This:

1. Dials the Lily TCP socket
2. Sends `#$# options +version +prompt +prompt2 +leaf-notify +leaf-cmd +connected`
3. Sends `<username> <password>` in response to the login prompt
4. Reads and applies all `%USER`, `%DISC`, `%GROUP`, `%DATA` messages during the `%SLCP-SYNC` block
5. Sends `/where me` to seed disc membership
6. Waits for `%connected` before returning

If Lily rejects the credentials, `conn.Connect()` returns an error and the proxy responds with HTTP 401. The password is never exposed further.

---

## Step 4 — Token generation

`generateToken()` reads 32 bytes from `crypto/rand` and hex-encodes them — 64 hex characters, 256 bits of entropy, completely unguessable and unrelated to the username.

---

## Step 5 — Session creation

A new `Session` is built holding the token, username, the `authVerifier` from step 2, and the Lily connection. Then:

```
sessions[token]      = sess
userTokens[username] = token
```

**No history is carried over from a previous session for this username.** A (re)connect always starts a fresh session, because a new Lily login replays the entire login sequence — banner, blurb and review prompts, entity sync — and restarts the message-ID counter. Pre-loading a prior event buffer would mismatch those IDs and suppress the very prompts (e.g. "Please enter a blurb") the user has to answer to finish logging in. The TUI keeps its own scrollback client-side, so nothing visible is lost.

---

## Step 6 — Token returned to the TUI

`handleAuth` responds with:

```json
{ "token": "<64-char hex string>" }
```

The TUI stores this token in its `Client` struct for all subsequent requests.

---

## Step 7 — Token required for all subsequent requests

Every endpoint other than `/auth` calls `sessionFromRequest`, which:

1. Checks the `token` query parameter (used by WebSocket since browsers cannot set custom headers during the HTTP upgrade)
2. Falls back to the `Authorization: Bearer <token>` HTTP header (used by all plain HTTP calls)

If the token is absent or not found in the `sessions` map, the handler returns HTTP 401 immediately. There is no other way to reach session data.

---

## Step 8 — WebSocket upgrade (`/ws`)

After obtaining the token, the TUI calls `client.Connect()`, which opens a WebSocket to:

```
ws://localhost:7888/ws?token=<token>
```

The proxy upgrades the connection, creates a `wsClient`, and adds it to `sess.subscribers`. The `fanOut` goroutine then delivers Lily server events to all subscribers over the WebSocket. The TUI's `listenCmd` goroutine delivers these to Bubble Tea as `serverEventMsg` values.

---

## Session teardown

When the Lily TCP connection closes (server-side disconnect or network failure), `fanOut` returns and:

1. Scheduled tasks (`%after` / `%every` / `%cron`) are stopped — their session is gone
2. `lily connection closed` is broadcast to all WebSocket subscribers
3. The session is removed from `sessions` and `userTokens`
4. `sess.conn.Close()` sends our FIN, so the Lily server reaps its side promptly rather than leaving the socket in `CLOSE_WAIT` — that is what lets an immediate reconnect log in instead of waiting for the old session to time out

Nothing is persisted. When the TUI reconnects it calls `/auth` again and gets a brand-new session and token (step 3 onward).

---

## History and catch-up within a session

Each session keeps a capped in-memory ring of recent events (`eventBuf`, `maxEventBuf` = 5000):

- **First subscriber** (`lastSeenID == 0`): `handleWS` replays the whole ring on connect, so a client that attaches after login still sees the text and interactive prompts that arrived before it got there.
- **Reconnecting subscriber**: the ring is not replayed. The client asks for what it missed via `GET /events?after=<lastSeenID>` (default 200, max 1000), and reports its position back with `POST /seen`.

Both live and die with the session; neither survives the Lily connection dropping.

---

## Security notes

- The proxy is intended to run **localhost-only**. Since all traffic is over loopback, the absence of TLS is acceptable for a local tool.
- If the proxy were ever exposed over a network, TLS would be required to protect both the password (step 1) and the bearer token (step 7).
- The existing-session shortcut in step 2 verifies the password before returning a token, so a username alone is never enough to reach a live session.
- Session tokens rotate on each fresh authentication (existing live sessions reuse their current token until they disconnect).
- Nothing is written to disk: credentials, the password verifier, and the event buffer exist only in the proxy's memory and are dropped when the session ends.
