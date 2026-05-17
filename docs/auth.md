# Authentication and Login Flow

This document describes the end-to-end login process for ZephyrLily, covering how the TUI client authenticates with the proxy and how the proxy authenticates with the Lily server.

## Overview

```
TUI client  ──POST /auth──▶  zlily-proxy  ──TCP──▶  Lily server
            ◀── token ───    (localhost)
            ──Bearer token──▶  (all subsequent requests)
```

The proxy runs as a localhost-only process. The TUI communicates with it over HTTP and WebSocket; the proxy maintains a persistent TCP connection to the Lily server on the TUI's behalf.

---

## Step 1 — TUI calls `POST /auth`

`cmd/tui/main.go` calls `client.Auth(username, password)`, which POSTs:

```json
{ "username": "josh", "password": "..." }
```

to the proxy's `/auth` endpoint. The password is never stored or logged by the proxy.

---

## Step 2 — Proxy checks for an existing session

`handleAuth` first looks up `username` in `userTokens` (an in-memory `username → token` map). If a live session already exists for that username, it returns the existing token immediately — no new Lily connection is opened and no new token is minted. This handles clients that call `/auth` again after a brief disconnect without tearing down the Lily TCP connection.

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

## Step 5 — Session creation and history restore

A new `Session` is built holding the token, username, and Lily connection. The proxy then checks `savedStates[username]` for a previously persisted event buffer and `lastSeenID` (kept across Lily TCP disconnects within the same proxy process). If found, the session is pre-loaded with that history before any client connects. Then:

```
sessions[token]      = sess
userTokens[username] = token
```

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

When the Lily TCP connection closes (server-side disconnect or network failure):

1. `fanOut` broadcasts an error message to all WebSocket subscribers
2. The event buffer and `lastSeenID` are persisted to `savedStates[username]`
3. The session is removed from `sessions` and `userTokens`

When the TUI reconnects and calls `/auth` again, step 5 restores the saved state so history and scroll position are preserved.

---

## Security notes

- The proxy is intended to run **localhost-only**. Since all traffic is over loopback, the absence of TLS is acceptable for a local tool.
- If the proxy were ever exposed over a network, TLS would be required to protect both the password (step 1) and the bearer token (step 7).
- Session tokens rotate on each fresh authentication (existing live sessions reuse their current token until they disconnect).
- The `savedStates` map is keyed by username (stable), not token, so history survives token rotation.
