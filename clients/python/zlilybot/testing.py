"""An in-process stand-in for the zlily proxy, for testing bots.

Speaks the real wire format from internal/proxy/api: the HTTP endpoints a
client needs plus a WebSocket carrying `WSServerMsg` envelopes. Hand-rolled
because the websockets library deliberately does not expose request bodies,
and /auth needs one.

Use it to drive a bot with no Lily server and no proxy binary::

    from zlilybot import ProxyRunner, Session
    from zlilybot.testing import FakeProxy

    async with FakeProxy() as proxy:
        session = Session(
            ProxyRunner(mode="attach", url=proxy.base_url), "bot", "pw"
        )
        bot = MyBot(session)
        task = asyncio.create_task(bot.run())
        proxy.push_event("public", "#2", recips=["#10"], value="hello")
        ...

`push_event`, `push_prompt`, `push_error` and `kill_session` script what the
proxy does; `received` records every line the bot sent back.
"""

from __future__ import annotations

import asyncio
import base64
import hashlib
import json
import os
import struct
from dataclasses import dataclass, field
from typing import Any
from urllib.parse import parse_qs, urlsplit

WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

# Sentinel status meaning "not ready yet; hold the connection open".
_DEFER = object()


@dataclass
class FakeSession:
    token: str
    username: str
    next_id: int = 1
    last_seen_id: int = 0
    buffer: list[dict] = field(default_factory=list)
    prompt: str | None = None
    prompt_id: int | None = None


class FakeProxy:
    """A scriptable proxy. Drive it with push_event / push_prompt / push_error."""

    def __init__(
        self,
        *,
        users: dict[str, str] | None = None,
        lily_addr: str = "fake.lily.org:7777",
        version: str = "0.0.0-fake",
        whoami: str = "#1",
        entities: list[dict] | None = None,
    ) -> None:
        self.users = users or {"bot": "pw"}
        self.lily_addr = lily_addr
        self.version = version
        self.whoami = whoami
        self.entities = entities if entities is not None else _default_entities()
        self.sessions: dict[str, FakeSession] = {}
        self.session_by_user: dict[str, FakeSession] = {}

        self.received: list[str] = []  # every line a client sent
        self.auth_calls = 0
        self.seen_calls: list[int] = []
        self.rename_should_fail = False
        # When set, /state blocks until a pending prompt is answered, the way
        # the real proxy waits on the SLCP sync (server.go handleState).
        self.state_gated_on_prompt = False
        # When set, a prompt is issued the moment a client opens the socket,
        # i.e. while its /state request is already in flight.
        self.prompt_on_ws_connect: str | None = None
        # Canned %begin/%end output, keyed by the command that triggers it.
        self.command_output: dict[str, list[str]] = {}

        self._server: asyncio.Server | None = None
        self._clients: set[asyncio.StreamWriter] = set()
        # Keep a strong reference: a task with no reference can be collected
        # before it runs, which loses broadcasts non-deterministically.
        self._pending: set[asyncio.Task] = set()
        self.base_url = ""
        self.line_event = asyncio.Event()

    # ── lifecycle ────────────────────────────────────────────────────────────

    async def start(self, host: str = "127.0.0.1", port: int = 0) -> str:
        self._server = await asyncio.start_server(self._serve, host, port)
        addr = self._server.sockets[0].getsockname()
        self.base_url = f"http://{addr[0]}:{addr[1]}"
        return self.base_url

    async def stop(self) -> None:
        for w in list(self._clients):
            w.close()
        self._clients.clear()
        if self._server is not None:
            self._server.close()
            await self._server.wait_closed()
            self._server = None

    async def __aenter__(self) -> "FakeProxy":
        await self.start()
        return self

    async def __aexit__(self, *exc) -> None:
        await self.stop()

    # ── scripting ────────────────────────────────────────────────────────────

    def _session(self) -> FakeSession:
        if not self.sessions:
            raise RuntimeError("no session yet; authenticate first")
        return next(iter(self.sessions.values()))

    def _emit(self, type_: str, data: Any, *, buffered: bool = True) -> int:
        s = self._session()
        msg = {"id": s.next_id, "type": type_, "data": data}
        s.next_id += 1
        if buffered:
            s.buffer.append(msg)
        task = asyncio.ensure_future(self._broadcast(msg))
        self._pending.add(task)
        task.add_done_callback(self._pending.discard)
        return msg["id"]

    def push_event(
        self,
        event: str,
        source: str,
        *,
        recips: list[str] | None = None,
        value: str = "",
        entities: dict | None = None,
        **extra,
    ) -> int:
        data = {
            "event": event,
            "source": source,
            "recips": recips or [],
            "value": value,
            "notify": True,
            "entities": entities if entities is not None else self._entity_map(),
            **extra,
        }
        return self._emit("event", data)

    def push_prompt(self, text: str) -> int:
        s = self._session()
        pid = self._emit("prompt", text)
        s.prompt, s.prompt_id = text, pid
        return pid

    def push_error(self, text: str) -> None:
        # Errors are never buffered by the real proxy.
        self._emit("error", text, buffered=False)

    def buffer_event(self, event: str, source: str, **kw) -> int:
        """Add history without notifying anyone -- i.e. before a client connects."""
        s = self._session()
        data = {
            "event": event, "source": source, "recips": kw.pop("recips", []) or [],
            "value": kw.pop("value", ""), "notify": True,
            "entities": self._entity_map(), **kw,
        }
        msg = {"id": s.next_id, "type": "event", "data": data}
        s.next_id += 1
        s.buffer.append(msg)
        return msg["id"]

    def set_name(self, handle: str, name: str) -> None:
        for e in self.entities:
            if e["handle"] == handle:
                e["name"] = name
                return

    def kill_session(self) -> None:
        """Drop the Lily link the way the real proxy does: notify, then close."""
        self.push_error("lily connection closed")
        self.sessions.clear()
        self.session_by_user.clear()
        for w in list(self._clients):
            w.close()
        self._clients.clear()

    async def wait_for_line(self, predicate, timeout: float = 3.0) -> str:
        """Block until a client sends a line satisfying `predicate`."""
        async def _wait():
            seen = 0
            while True:
                for line in self.received[seen:]:
                    if predicate(line):
                        return line
                seen = len(self.received)
                self.line_event.clear()
                await self.line_event.wait()

        return await asyncio.wait_for(_wait(), timeout)

    def _entity_map(self) -> dict:
        return {e["handle"]: {k: v for k, v in e.items() if k != "handle"}
                for e in self.entities}

    # ── HTTP ─────────────────────────────────────────────────────────────────

    async def _serve(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        try:
            request_line = await reader.readline()
            if not request_line:
                return
            method, raw_path, _ = request_line.decode().split(" ", 2)

            headers: dict[str, str] = {}
            while True:
                line = await reader.readline()
                if line in (b"\r\n", b"\n", b""):
                    break
                k, _, v = line.decode().partition(":")
                headers[k.strip().lower()] = v.strip()

            body = b""
            if length := int(headers.get("content-length", 0) or 0):
                body = await reader.readexactly(length)

            parts = urlsplit(raw_path)
            query = parse_qs(parts.query)

            if parts.path == "/ws" and "upgrade" in headers.get("connection", "").lower():
                await self._handle_ws(reader, writer, headers, query)
                return

            status, payload = self._route(method, parts.path, query, headers, body)
            while status is _DEFER:
                # Hold the request open the way a blocking /state does.
                await asyncio.sleep(0.02)
                status, payload = self._route(method, parts.path, query, headers, body)
            await self._respond(writer, status, payload)
        except (asyncio.IncompleteReadError, ConnectionResetError):
            pass
        finally:
            if not writer.is_closing():
                writer.close()

    def _route(self, method, path, query, headers, body) -> tuple[int, Any]:
        if path == "/info":
            return 200, {"lily_addr": self.lily_addr, "version": self.version}

        if path == "/auth":
            self.auth_calls += 1
            data = json.loads(body or b"{}")
            user, pw = data.get("username"), data.get("password")
            if self.users.get(user) != pw:
                return 401, {"error": "bad credentials"}
            if (existing := self.session_by_user.get(user)) is not None:
                return 200, {"token": existing.token}
            token = os.urandom(16).hex()
            s = FakeSession(token=token, username=user)
            self.sessions[token] = s
            self.session_by_user[user] = s
            return 200, {"token": token}

        session = self._auth(query, headers)
        if session is None:
            return 401, {"error": "unauthorized"}

        if path == "/session":
            return 200, {"username": session.username, "lily_addr": self.lily_addr}

        if path == "/state":
            if self.state_gated_on_prompt and session.prompt is not None:
                return _DEFER, None
            out = {
                "whoami": self.whoami,
                "version": "2.4",
                "server": self.lily_addr,
                "last_seen_id": session.last_seen_id,
                "event_buf_size": len(session.buffer),
                "entities": self.entities,
            }
            if session.prompt is not None:
                out["prompt"] = session.prompt
                out["prompt_id"] = session.prompt_id
            return 200, out

        if path == "/events":
            after = int(query.get("after", ["0"])[0])
            limit = int(query.get("limit", ["200"])[0])
            matching = [m for m in session.buffer if m["id"] > after]
            page = matching[:limit]
            return 200, {"events": page, "more": len(matching) > len(page)}

        if path == "/seen":
            data = json.loads(body or b"{}")
            value = int(data.get("last_seen_id", 0))
            self.seen_calls.append(value)
            session.last_seen_id = max(session.last_seen_id, value)
            return 204, None

        return 404, {"error": "not found"}

    def _auth(self, query, headers) -> FakeSession | None:
        token = (query.get("token") or [None])[0]
        if token is None:
            auth = headers.get("authorization", "")
            if auth.lower().startswith("bearer "):
                token = auth[7:]
        return self.sessions.get(token) if token else None

    async def _respond(self, writer, status: int, payload: Any) -> None:
        reason = {200: "OK", 204: "No Content", 401: "Unauthorized", 404: "Not Found"}
        body = b"" if payload is None else json.dumps(payload).encode()
        head = (
            f"HTTP/1.1 {status} {reason.get(status, 'Error')}\r\n"
            f"Content-Type: application/json\r\n"
            f"Content-Length: {len(body)}\r\n"
            f"Connection: close\r\n\r\n"
        ).encode()
        writer.write(head + body)
        await writer.drain()

    # ── WebSocket ────────────────────────────────────────────────────────────

    async def _handle_ws(self, reader, writer, headers, query) -> None:
        session = self._auth(query, headers)
        if session is None:
            await self._respond(writer, 401, {"error": "unauthorized"})
            return

        key = headers.get("sec-websocket-key", "")
        accept = base64.b64encode(
            hashlib.sha1((key + WS_GUID).encode()).digest()
        ).decode()
        writer.write(
            b"HTTP/1.1 101 Switching Protocols\r\n"
            b"Upgrade: websocket\r\nConnection: Upgrade\r\n"
            b"Sec-WebSocket-Accept: " + accept.encode() + b"\r\n\r\n"
        )
        await writer.drain()
        self._clients.add(writer)

        # The real proxy replays its whole buffer to a client that has never
        # reported a read position.
        if session.last_seen_id == 0:
            for msg in list(session.buffer):
                await self._send_frame(writer, json.dumps(msg))

        if self.prompt_on_ws_connect is not None:
            text, self.prompt_on_ws_connect = self.prompt_on_ws_connect, None
            self.push_prompt(text)

        try:
            while True:
                frame = await self._read_frame(reader)
                if frame is None:
                    break
                opcode, payload = frame
                if opcode == 0x8:
                    break
                if opcode == 0x9:
                    await self._send_frame(writer, payload.decode(), opcode=0xA)
                    continue
                if opcode != 0x1:
                    continue
                data = json.loads(payload.decode())
                if data.get("type") == "command":
                    await self._on_command(session, data.get("text", ""))
        except (asyncio.IncompleteReadError, ConnectionResetError):
            pass
        finally:
            self._clients.discard(writer)
            if not writer.is_closing():
                writer.close()

    async def _on_command(self, session: FakeSession, text: str) -> None:
        self.received.append(text)
        self.line_event.set()

        # A line forwarded to Lily clears any pending prompt.
        if not text.startswith("%"):
            session.prompt = session.prompt_id = None

        if text.startswith("/") and not text.startswith("/rename "):
            # Lily frames a command's output in %begin/%end; the proxy collapses
            # that into one commandresult.
            lines = self.command_output.get(text)
            if lines is not None:
                self._emit("commandresult", {"cmd_id": len(self.received), "lines": lines})

        if text.startswith("/rename ") and not self.rename_should_fail:
            new = text[len("/rename "):].strip()
            self.set_name(self.whoami, new)
            self.push_event("rename", self.whoami, value=new)

    async def _broadcast(self, msg: dict) -> None:
        payload = json.dumps(msg)
        for w in list(self._clients):
            try:
                await self._send_frame(w, payload)
            except Exception:
                self._clients.discard(w)

    async def _send_frame(self, writer, text: str, opcode: int = 0x1) -> None:
        data = text.encode()
        header = bytes([0x80 | opcode])
        n = len(data)
        if n < 126:
            header += bytes([n])
        elif n < 1 << 16:
            header += bytes([126]) + struct.pack("!H", n)
        else:
            header += bytes([127]) + struct.pack("!Q", n)
        writer.write(header + data)
        await writer.drain()

    async def _read_frame(self, reader) -> tuple[int, bytes] | None:
        head = await reader.readexactly(2)
        opcode = head[0] & 0x0F
        masked = bool(head[1] & 0x80)
        length = head[1] & 0x7F
        if length == 126:
            length = struct.unpack("!H", await reader.readexactly(2))[0]
        elif length == 127:
            length = struct.unpack("!Q", await reader.readexactly(8))[0]
        mask = await reader.readexactly(4) if masked else b""
        payload = await reader.readexactly(length)
        if masked:
            payload = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        return opcode, payload


def _default_entities() -> list[dict]:
    return [
        {"handle": "#1", "kind": "user", "name": "bot", "state": "here"},
        {"handle": "#2", "kind": "user", "name": "Alice", "state": "here"},
        {"handle": "#3", "kind": "user", "name": "Bob", "state": "here"},
        {"handle": "#10", "kind": "disc", "name": "test", "title": "Testing"},
        {"handle": "#11", "kind": "disc", "name": "emotes", "attrib": "emote"},
    ]
