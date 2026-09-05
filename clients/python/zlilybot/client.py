"""HTTP + WebSocket client for the zlily proxy API.

See docs/client-api.md. The two reference implementations are
web/src/lib/ws.js (smallest) and internal/tui/client/client.go (complete).
"""

from __future__ import annotations

import json
import logging
from dataclasses import dataclass
from typing import Any, AsyncIterator, Sequence
from urllib.parse import urlsplit, urlunsplit

import httpx
from websockets.asyncio.client import ClientConnection, connect

from .errors import AuthError, ConfigError, Disconnected, TransientAuthError
from .events import Entity, ServerMessage

log = logging.getLogger(__name__)

# The proxy caps a history page at 1000 (internal/proxy/api/server.go).
EVENTS_PAGE = 1000

# GET /state blocks server-side until the SLCP sync completes, for up to 60s
# (internal/proxy/api/server.go handleState). Our own deadline has to clear
# that, or we abandon a login that was about to succeed.
STATE_TIMEOUT = 90.0

# /fetch, /store and /shorten each wait on a round trip to Lily or to an
# outside service; the proxy gives up at 10s, so clear that before failing.
FETCH_TIMEOUT = 20.0


def normalize_base_url(addr: str) -> str:
    """Accept `host:port` or a full URL and return an `http(s)://host:port` base.

    The proxy must sit at the host root — internal/tui/client/addr.go rejects a
    URL with a path, so a reverse-proxy sub-path mount cannot work and is
    refused here rather than failing later with a confusing 404.
    """
    addr = addr.strip()
    if not addr:
        raise ConfigError("proxy address is empty")

    if "://" not in addr:
        addr = "http://" + addr

    parts = urlsplit(addr)
    scheme = {"ws": "http", "wss": "https"}.get(parts.scheme, parts.scheme)
    if scheme not in ("http", "https"):
        raise ConfigError(f"unsupported proxy scheme {parts.scheme!r} in {addr!r}")
    if not parts.hostname:
        raise ConfigError(f"proxy address {addr!r} has no host")
    if parts.path.strip("/"):
        raise ConfigError(
            f"proxy address {addr!r} has a URL path; the proxy must be served "
            "at the host root"
        )
    return urlunsplit((scheme, parts.netloc, "", "", ""))


@dataclass
class State:
    """The `GET /state` snapshot."""

    whoami: str
    version: str
    server: str
    last_seen_id: int
    event_buf_size: int
    entities: list[Entity]
    prompt: str | None = None
    prompt_id: int | None = None

    @classmethod
    def from_json(cls, d: dict[str, Any]) -> "State":
        return cls(
            whoami=d.get("whoami", ""),
            version=d.get("version", ""),
            server=d.get("server", ""),
            last_seen_id=d.get("last_seen_id", 0) or 0,
            event_buf_size=d.get("event_buf_size", 0) or 0,
            entities=[Entity.from_json(e) for e in (d.get("entities") or [])],
            prompt=d.get("prompt"),
            prompt_id=d.get("prompt_id"),
        )


class ZlilyClient:
    """One authenticated conversation with a zlily proxy.

    A client owns a token and, once connected, a WebSocket. It does not retry or
    reconnect; that is the supervisor's job in session.py.
    """

    def __init__(
        self,
        base_url: str,
        *,
        timeout: float = 15.0,
        state_timeout: float = STATE_TIMEOUT,
    ) -> None:
        self.base_url = normalize_base_url(base_url)
        self.token: str | None = None
        self.state_timeout = state_timeout
        self._http = httpx.AsyncClient(base_url=self.base_url, timeout=timeout)
        self._ws: ClientConnection | None = None

    async def aclose(self) -> None:
        await self.close_ws()
        await self._http.aclose()

    # ── HTTP ─────────────────────────────────────────────────────────────────

    @property
    def _auth_headers(self) -> dict[str, str]:
        if not self.token:
            raise ZlilyClientNotAuthenticated("no token; call auth() first")
        return {"Authorization": f"Bearer {self.token}"}

    async def info(self) -> dict[str, Any]:
        """`GET /info` — what this proxy is and what it fronts. Needs no token.

        Returns `lily_addr` and `version`. This is the only thing a client can
        ask before authenticating, so it is where a separately-released client
        checks what it is talking to.
        """
        r = await self._http.get("/info")
        r.raise_for_status()
        return r.json()

    async def auth(self, username: str, password: str) -> str:
        """`POST /auth` — log in and keep the token.

        Returns the token. A live session for this user returns the *same*
        token and the message-id sequence continues; a fresh one restarts ids
        at 1, so callers must drop a stored highwater when the token changes.
        """
        r = await self._http.post(
            "/auth", json={"username": username, "password": password}
        )
        if r.status_code == 401:
            raise AuthError(
                f"lily rejected the credentials for {username!r} "
                "(or they do not match an existing live session)"
            )
        if r.status_code == 429:
            raise TransientAuthError(
                "proxy is rate limiting logins from this address",
                retry_after=_retry_after(r),
            )
        if r.status_code == 503:
            raise TransientAuthError("proxy is at its session capacity")
        r.raise_for_status()
        self.token = r.json()["token"]
        return self.token

    async def session(self) -> dict[str, Any]:
        """`GET /session` — cheap liveness check for a token we already hold."""
        r = await self._http.get("/session", headers=self._auth_headers)
        r.raise_for_status()
        return r.json()

    async def state(self) -> State:
        """Fetch the state snapshot.

        This blocks until the server-side sync finishes. A prompt pending at
        login gates that sync, so a caller must already be answering prompts on
        the WebSocket by the time it gets here -- see Session._establish.
        """
        r = await self._http.get(
            "/state", headers=self._auth_headers, timeout=self.state_timeout
        )
        r.raise_for_status()
        return State.from_json(r.json())

    async def events(self, after: int = 0, limit: int = EVENTS_PAGE) -> tuple[list[ServerMessage], bool]:
        r = await self._http.get(
            "/events",
            params={"after": after, "limit": min(limit, EVENTS_PAGE)},
            headers=self._auth_headers,
        )
        r.raise_for_status()
        body = r.json()
        msgs = [ServerMessage.from_json(m) for m in (body.get("events") or [])]
        return msgs, bool(body.get("more"))

    async def scan_history(self) -> tuple[int, ServerMessage | None]:
        """Read the whole buffer, reporting its end and any prompt still open.

        Two things come from one pass. The highest id is the replay highwater:
        on a fresh session the proxy re-sends its entire ring buffer over the
        WebSocket, and a bot that answered all of it would relitigate a day of
        conversation.

        The prompt matters because it may predate our connection, in which case
        it only reaches us through that replay -- which the highwater is about
        to discard. A prompt counts as open when no line was forwarded after it:
        the proxy clears its pending prompt on the next forwarded line, and it
        publishes every such line as an `input` message.
        """
        after, highest = 0, 0
        pending: ServerMessage | None = None
        while True:
            msgs, more = await self.events(after=after)
            for msg in msgs:
                if msg.type == "prompt":
                    pending = msg
                elif msg.type == "input":
                    pending = None
            if msgs:
                highest = max(highest, msgs[-1].id)
                after = msgs[-1].id
            if not more or not msgs:
                return highest, pending

    async def mark_seen(self, last_id: int) -> None:
        """`POST /seen` — record our read position so a restart can resume.

        The proxy takes the maximum, so an out-of-order call is harmless.
        """
        r = await self._http.post(
            "/seen", json={"last_seen_id": last_id}, headers=self._auth_headers
        )
        r.raise_for_status()

    # ── stored content ───────────────────────────────────────────────────────

    async def fetch(
        self, kind: str = "info", target: str = "me", name: str = ""
    ) -> list[str]:
        """`GET /fetch` — read a user's /info or a named memo.

        Lines come back with Lily's `* ` prefix already stripped, and an empty
        list means there is no such content. Useful for keeping a bot's data
        on the server, where it can be edited by anyone with access rather
        than only by whoever can reach the bot's filesystem.
        """
        params: dict[str, str] = {"type": kind, "target": target}
        if name:
            params["name"] = name
        r = await self._http.get(
            "/fetch", params=params, headers=self._auth_headers, timeout=FETCH_TIMEOUT
        )
        r.raise_for_status()
        return list(r.json().get("lines") or [])

    async def store(
        self,
        lines: Sequence[str],
        kind: str = "info",
        target: str = "me",
        name: str = "",
    ) -> None:
        """`POST /store` — replace a user's /info or a named memo.

        The proxy allows one store at a time per session and answers 409 while
        another is in flight.
        """
        r = await self._http.post(
            "/store",
            json={"type": kind, "target": target, "name": name, "lines": list(lines)},
            headers=self._auth_headers,
            timeout=FETCH_TIMEOUT,
        )
        r.raise_for_status()

    # ── names and links ──────────────────────────────────────────────────────

    async def expand(self, partial: str, valid_dest_only: bool = False) -> list[Entity]:
        """`GET /expand` — find entities whose name matches a partial string.

        Exact matches come first, then prefix matches. The reference clients
        expand only when there is exactly one match, treating none or several
        as "leave it alone".
        """
        params: dict[str, str] = {"q": partial}
        if valid_dest_only:
            params["valid_dest_only"] = "1"
        r = await self._http.get("/expand", params=params, headers=self._auth_headers)
        r.raise_for_status()
        return [Entity.from_json(e) for e in (r.json().get("matches") or [])]

    async def shorten(self, url: str, service: str = "") -> str:
        """`POST /shorten` — shorten a URL through the proxy.

        Going through the proxy rather than calling a shortener directly means
        a bot inherits whatever service and API key the proxy was built with,
        and gains nothing to configure. An empty `service` takes the default.
        """
        r = await self._http.post(
            "/shorten",
            json={"service": service, "url": url},
            headers=self._auth_headers,
            timeout=FETCH_TIMEOUT,
        )
        r.raise_for_status()
        return r.json().get("short", "")

    # ── WebSocket ────────────────────────────────────────────────────────────

    def _ws_url(self) -> str:
        parts = urlsplit(self.base_url)
        scheme = "wss" if parts.scheme == "https" else "ws"
        return urlunsplit((scheme, parts.netloc, "/ws", f"token={self.token}", ""))

    async def connect_ws(self) -> None:
        if not self.token:
            raise ZlilyClientNotAuthenticated("no token; call auth() first")
        # max_size=None: command results can be large, and the Go client
        # likewise lifts its read limit. ping_interval gives us liveness
        # detection, since the proxy never sends WebSocket control frames.
        self._ws = await connect(
            self._ws_url(), max_size=None, ping_interval=20, ping_timeout=20
        )

    async def close_ws(self) -> None:
        ws, self._ws = self._ws, None
        if ws is not None:
            try:
                await ws.close()
            except Exception:  # noqa: BLE001 - closing a dead socket is fine
                pass

    async def send(self, line: str) -> None:
        """Forward a raw input line to Lily.

        `"command"` is the only client message type the proxy accepts. An empty
        line is valid and answers a pending prompt — that is how the bot hits
        enter at a `%prompt`.
        """
        if self._ws is None:
            raise Disconnected("websocket is not connected")
        await self._ws.send(json.dumps({"type": "command", "text": line}))

    async def stream(self) -> AsyncIterator[ServerMessage]:
        if self._ws is None:
            raise Disconnected("websocket is not connected")
        async for raw in self._ws:
            if isinstance(raw, bytes):
                raw = raw.decode("utf-8", "replace")
            try:
                payload = json.loads(raw)
            except ValueError:
                log.warning("discarding unparseable frame from proxy: %.120r", raw)
                continue
            yield ServerMessage.from_json(payload)


class ZlilyClientNotAuthenticated(Disconnected):
    """A call needing a token was made before auth()."""


def _retry_after(r: httpx.Response) -> float | None:
    raw = r.headers.get("Retry-After")
    if not raw:
        return None
    try:
        return float(raw)
    except ValueError:
        return None
