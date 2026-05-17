# ZephyrLily Proxy — Client API

This document describes everything a client needs to connect to `zlily-proxy` and interact with the Lily server through it. The proxy exposes a JSON HTTP API and a JSON WebSocket stream.

All communication is currently over plain HTTP/WebSocket on localhost. See [auth.md](auth.md) for security notes.

---

## Authentication

All endpoints except `POST /auth` require a bearer token obtained at login.

Pass the token in one of two ways:

```
Authorization: Bearer <token>          # HTTP header (preferred for HTTP requests)
?token=<token>                         # query parameter (required for WebSocket)
```

Missing or unrecognised tokens return **HTTP 401**.

---

## Endpoints

### `POST /auth` — Authenticate

Connects to the Lily server and returns a session token.  
If the user already has an active session the existing token is returned (no new connection is made).

**Request body**
```json
{ "username": "josh", "password": "s3cr3t" }
```

**Response `200 OK`**
```json
{ "token": "a3f8..." }
```
The token is a 64-character hex string (32 random bytes). Store it and include it on every subsequent request.

**Errors**
| Status | Meaning |
|--------|---------|
| 400 | Missing username or password |
| 401 | Lily rejected the credentials |
| 405 | Method not POST |

---

### `GET /state` — Initial state snapshot

Returns the current Lily state: the client's own identity, the server name and version, and the full entity list (users, discussions, groups).  
Fetch this once after authentication, before opening the WebSocket.

**Response `200 OK`**
```json
{
  "whoami":        "#850",
  "version":       "2.4",
  "server":        "rpi.lily.org",
  "last_seen_id":  142,
  "event_buf_size": 500,
  "entities": [
    {
      "handle":   "#850",
      "kind":     "user",
      "name":     "Josh Wilmes",
      "blurb":    "working on things",
      "state":    "here",
      "pronoun":  "he/him"
    },
    {
      "handle":   "#12",
      "kind":     "disc",
      "name":     "emacs",
      "title":    "The Emacs Discussion",
      "attrib":   "m",
      "creation": 1234567890
    },
    {
      "kind":    "group",
      "name":    "abcd",
      "members": ["#850", "#23", "#41"]
    }
  ]
}
```

**Entity kinds**

| `kind` | Description | Notable fields |
|--------|-------------|----------------|
| `"user"` | Connected user | `blurb`, `state` (`"here"`/`"away"`), `pronoun` |
| `"disc"` | Discussion | `title`, `attrib` (`"m"` = current user is a member), `creation` (unix ts) |
| `"group"` | Named group | `members` (list of user handles) |

`last_seen_id` is the highest event ID the proxy last recorded for this user — use it to restore scroll position and to seed the `/events` history fetch.  
`event_buf_size` is the number of events currently in the proxy's ring buffer.

---

### `GET /events` — Buffered event history

Returns events from the proxy's ring buffer (up to 5 000 events). Use this on startup to replay missed events before connecting the WebSocket.

**Query parameters**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `after`   | `0`     | Return only events with `id > after` |
| `limit`   | `200`   | Maximum events to return (max 1 000) |

**Response `200 OK`**
```json
{
  "events": [ /* array of WSServerMsg — same format as WebSocket messages */ ],
  "more":   true
}
```

`more: true` means there are additional events beyond the limit. Page through by setting `after` to the last event's `id` and repeating until `more` is false.

**Typical startup sequence**

```
afterID = state.last_seen_id
loop:
    GET /events?after={afterID}&limit=200
    append events to local buffer
    if not more: break
    afterID = events.last.id
open WebSocket
```

---

### WebSocket `GET /ws` — Live event stream

Upgrades to a WebSocket connection. Provide the token as a query parameter:

```
ws://localhost:7888/ws?token=<token>
```

Multiple clients may connect simultaneously for the same session (all receive the same stream).

#### Messages from the proxy → client (`WSServerMsg`)

Every message has the shape:

```json
{ "id": 42, "type": "<type>", "data": <payload> }
```

`id` is a monotonically increasing integer, unique per session. It matches the `id` values in `/events` history.

| `type` | `data` shape | Description |
|--------|-------------|-------------|
| `"event"` | `EventData` | A structured Lily event (message, join, rename, …) |
| `"text"` | `{"text": "…"}` | A raw unformatted text line from the server |
| `"commandresult"` | `{"cmd_id": N, "lines": […]}` | Buffered output from a `/command` (sent as one block) |
| `"prompt"` | `"text of prompt"` | The current input prompt string |
| `"error"` | `"description"` | A proxy-generated error (e.g. keepalive timeout, disconnection) |

#### `EventData` (type `"event"`)

```json
{
  "event":    "public",
  "source":   "#23",
  "time":     1716000000,
  "value":    "hello everyone",
  "recips":   ["#850", "#41"],
  "targets":  [],
  "sub_evt":  "",
  "notify":   true,
  "stamp":    true,
  "entities": {
    "#23": { "handle": "#23", "kind": "user", "name": "Alice", "blurb": "…" }
  }
}
```

| Field | Description |
|-------|-------------|
| `event` | Event type (see below) |
| `source` | Handle of the entity that caused the event |
| `time` | Unix timestamp (only meaningful when `stamp` is true) |
| `value` | Event-specific text (message body, new name, blurb text, …) |
| `recips` | Recipient handles (discussions or users the event targets) |
| `notify` | Whether the event should be displayed to the user |
| `stamp` | Whether `time` should be shown in the UI |
| `entities` | Snapshot of entity records referenced by this event (use for display; keyed by handle) |
| `text` | Pre-formatted plain-text string ready to display. For simple events this is the complete message (e.g. `*** Alice is now "here" ***`). For `public`, `private`, `emote`, and `pa` it is a compact single-line summary; rich clients should override those with structured formatting. See [events.md](events.md) for examples of every event type. |

**Common event types**

See [events.md](events.md) for the full reference: display format, field semantics, and required state updates for every event type.

| `event` | Meaning | Relevant fields |
|---------|---------|-----------------|
| `"public"` | Public send | `source`, `recips`, `value` (message) |
| `"private"` | Private send | `source`, `recips`, `value` |
| `"emote"` | Emote action | `source`, `recips`, `value` |
| `"connect"` | User logged in | `source` |
| `"disconnect"` | User logged out | `source`, `value` (reason) |
| `"here"` / `"away"` | Presence change | `source` |
| `"rename"` | User renamed | `source`, `value` (new name) |
| `"blurb"` | Blurb changed | `source`, `value` (new blurb, empty = cleared) |
| `"unidle"` | User became active | `source` |
| `"join"` | User joined disc | `source`, `recips` (disc handles) |
| `"quit"` | User left disc | `source`, `recips` |
| `"create"` | Disc created | `source`, `recips` (new disc handle) |
| `"destroy"` | Disc destroyed | `source`, `recips` |
| `"retitle"` | Disc title changed | `source`, `recips`, `value` (new title) |
| `"drename"` | Disc renamed | `recips`, `value` (new name) |
| `"sysmsg"` | Server system message | `value` |
| `"pa"` | Public address | `source`, `value` |

#### Messages from client → proxy (`WSClientMsg`)

```json
{ "type": "command", "text": "josh;hello there" }
```

The `text` field is forwarded verbatim to the Lily server. Commands starting with `%` are intercepted by the proxy and handled locally (see [Proxy-side commands](#proxy-side-commands)); all other text is forwarded to Lily.

An empty `text` is valid and sends a blank line to Lily (used to re-prompt).

---

### `POST /seen` — Report scroll position

Tells the proxy how far the user has scrolled. The proxy persists this so a reconnecting client can restore scroll position. Only stores the value if it is higher than the current stored value (never decreases).

**Request body**
```json
{ "last_seen_id": 142 }
```

**Response `204 No Content`** on success.

Call this periodically (e.g. every 5 seconds) while the scroll position advances.

---

### `GET /expand` — Name expansion

Searches the proxy's entity state for names matching a partial string. Use this to implement tab-completion and intelligent-expand (`:`, `;`, `,`, `=` key bindings).

**Query parameters**

| Parameter | Description |
|-----------|-------------|
| `q` | Partial name to search (case-insensitive) |
| `valid_dest_only=1` | Exclude discussions the current user is not a member of |

The proxy returns exact matches first; if none, prefix matches.

**Response `200 OK`**
```json
{
  "matches": [
    { "handle": "#23", "kind": "user", "name": "Alice Brown", … }
  ]
}
```

**Recommended client behaviour (matching the reference)**
- 1 match → expand to it; replace spaces with underscores; prefix disc names with `-`
- 0 or >1 matches → no expansion (ambiguous or not found)

---

### `GET /fetch` — Fetch info or memo content

Retrieves the current content of a user's `/info` file or a named memo. The proxy sends the appropriate Lily command and parses the response.

**Query parameters**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `type` | `"info"` | `"info"` or `"memo"` |
| `target` | `"me"` | Handle or name of the target user/discussion |
| `name` | — | Memo name (required when `type=memo`) |

**Response `200 OK`**
```json
{ "lines": ["first line of info", "second line", "…"] }
```

Content lines have already had the Lily `* ` prefix stripped. An empty `lines` array means no existing content.

**Errors**
| Status | Meaning |
|--------|---------|
| 400 | Unknown type |
| 409 | Another fetch is already in progress |
| 504 | Lily did not respond within 10 seconds |

---

### `POST /store` — Store info or memo content

Stores new content for a user's `/info` file or a named memo using the Lily `export_file` protocol.

**Request body**
```json
{
  "type":   "info",
  "target": "me",
  "name":   "",
  "lines":  ["first line", "second line"]
}
```

Use `"target": ""` or `"target": "me"` to store content for the current user.  
For memos, `"name"` is required.

**Response `204 No Content`** on success.

**Errors**
| Status | Meaning |
|--------|---------|
| 400 | Unknown type or bad request |
| 409 | Another store is already in progress |
| 502 | Lily rejected the store (server returned ERROR) |
| 504 | Lily did not respond within 10 seconds |

---

## Proxy-side commands (`%command`)

Commands sent over the WebSocket with `text` starting with `%` are handled by the proxy, not forwarded to Lily. They return a `commandresult` message.

| Command | Description |
|---------|-------------|
| `%help` | List all proxy-side commands |
| `%help <topic>` | Detailed help for a topic |
| `%version` | Proxy version |
| `%whoami` | Your handle and display name |
| `%server` | Connected server name and version |
| `%debug` | Inspect proxy-side state |
| `%debug discs` | List all discussions with membership status |
| `%debug users` | List all known users |
| `%debug groups` | List all known groups |
| `%debug all` | Show everything |
| `%info` | Placeholder (implement client-side using `/fetch` + `/store`) |
| `%memo` | Placeholder (implement client-side using `/fetch` + `/store`) |

---

## Client implementation checklist

A complete client must:

- [ ] Call `POST /auth` and store the returned token securely
- [ ] Call `GET /state` to obtain `whoami`, the entity list, `last_seen_id`, and `event_buf_size`
- [ ] Page through `GET /events?after={last_seen_id}` until `more` is false to replay history
- [ ] Open a WebSocket to `/ws?token=<token>` and handle all incoming message types
- [ ] Send commands over the WebSocket as `{"type":"command","text":"…"}`
- [ ] Periodically call `POST /seen` with the highest displayed message ID
- [ ] Handle `"error"` messages (keepalive timeout, Lily TCP disconnection)
- [ ] On reconnect, call `POST /auth` again (returns the same token if still alive), re-fetch state, and replay events since the stored `last_seen_id`
- [ ] Implement `%info edit` / `%memo edit` using `GET /fetch` then `POST /store` (the proxy stubs return an error for clients that send `%info` or `%memo` directly)
- [ ] Display only events where `notify: true`; show timestamps only when `stamp: true`

Optional but recommended:

- [ ] Call `GET /expand?q=<partial>&valid_dest_only=1` to power name completion
- [ ] Use the `entities` map embedded in each event for display names rather than looking up the entity list separately
