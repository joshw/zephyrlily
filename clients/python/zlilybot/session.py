"""A durable Lily session: log in, keep the name, skip prompts, reconnect.

`Session.messages()` is a single async iterator that survives the link dropping.
Everything a bot needs to stay online unattended lives behind it.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import random
import time
from typing import AsyncIterator, Awaitable, Callable, Sequence

from .client import State, ZlilyClient
from .entities import Entities, same_name, to_lily_name
from .errors import AuthError, Disconnected, NameUnavailable, TransientAuthError
from .events import Entity, ServerMessage
from .proxy import ProxyRunner

log = logging.getLogger(__name__)

COMMAND_TIMEOUT = 5.0
RENAME_TIMEOUT = 8.0
RENAME_POLL = 0.25
SEEN_INTERVAL = 5.0

# A prompt storm means something upstream is wrong; answering it as fast as it
# arrives would spin. Warn rather than hammer.
PROMPT_BURST = 10
PROMPT_BURST_WINDOW = 30.0

# The proxy signals a dead Lily link this way, then drops the session.
LILY_CLOSED = "lily connection closed"


class Session:
    def __init__(
        self,
        runner: ProxyRunner,
        username: str,
        password: str,
        *,
        name: str | None = None,
        join: Sequence[str] = (),
        on_ready: Callable[["Session"], Awaitable[None]] | None = None,
        max_backoff: float = 60.0,
    ) -> None:
        self.runner = runner
        self.username = username
        self.password = password
        self.desired_name = name or None
        self.join = tuple(join)
        self.on_ready = on_ready
        self.max_backoff = max_backoff

        self.entities = Entities()
        self.whoami: str = ""
        self.state: State | None = None
        self.client: ZlilyClient | None = None

        self._token: str | None = None
        self._highwater = 0
        self._reported_seen = 0
        self._answered_prompts: set[int] = set()
        self._prompt_times: list[float] = []
        self._stopped = False
        self._seen_task: asyncio.Task | None = None
        self._command_lock = asyncio.Lock()
        self._command_result: asyncio.Future[list[str]] | None = None

    # ── outbound ─────────────────────────────────────────────────────────────

    async def send(self, line: str) -> None:
        """Send one raw input line to Lily.

        Text may be Unicode. SLCP itself carries 7-bit ASCII, but the proxy
        folds every outbound line in `Conn.Send`, so clients need not.
        """
        if self.client is None:
            raise Disconnected("session is not connected")
        await self.client.send(line)

    async def run_command(
        self, line: str, timeout: float = COMMAND_TIMEOUT
    ) -> list[str]:
        """Send a Lily command and collect the output it produces.

        Lily frames a command's output in %begin/%end, which the proxy delivers
        as a single `commandresult`. There is no id we can set on the way out --
        the id comes from Lily -- so this claims the next result to arrive. That
        is sound because commands are serialised on the connection and a bot has
        its own session, and the lock keeps us from having two in flight.

        Returns the output lines, or an empty list if the command produced no
        framed output before the timeout. Some Lily commands do not leaf, so an
        empty result does not mean the command failed.
        """
        async with self._command_lock:
            loop = asyncio.get_running_loop()
            self._command_result = loop.create_future()
            try:
                await self.send(line)
                return await asyncio.wait_for(self._command_result, timeout)
            except (asyncio.TimeoutError, TimeoutError):
                log.debug("no framed output for %r within %.0fs", line, timeout)
                return []
            finally:
                self._command_result = None

    async def say(self, dest: str, text: str) -> None:
        await self.send(f"{dest};{text}")

    async def emote(self, dest: str, text: str) -> None:
        await self.send(f"{dest};:{text}")

    # ── stored content, names and links ──────────────────────────────────────
    #
    # Thin passthroughs, so a bot never has to reach past the session for the
    # ordinary things. Each raises Disconnected rather than AttributeError when
    # called before the session is up.

    async def fetch(
        self, kind: str = "info", target: str = "me", name: str = ""
    ) -> list[str]:
        """Read a user's /info or a named memo."""
        return await self._client().fetch(kind=kind, target=target, name=name)

    async def store(
        self,
        lines: Sequence[str],
        kind: str = "info",
        target: str = "me",
        name: str = "",
    ) -> None:
        """Replace a user's /info or a named memo."""
        await self._client().store(lines, kind=kind, target=target, name=name)

    async def expand(self, partial: str, valid_dest_only: bool = False) -> list[Entity]:
        """Find entities whose name matches a partial string."""
        return await self._client().expand(partial, valid_dest_only=valid_dest_only)

    async def shorten(self, url: str, service: str = "") -> str:
        """Shorten a URL using the proxy's configured service."""
        return await self._client().shorten(url, service=service)

    def _client(self) -> ZlilyClient:
        if self.client is None:
            raise Disconnected("session is not connected")
        return self.client

    @property
    def my_name(self) -> str:
        return self.entities.name(self.whoami) if self.whoami else ""

    # ── the message stream ───────────────────────────────────────────────────

    async def messages(self) -> AsyncIterator[ServerMessage]:
        """Yield messages forever, re-establishing the session as needed.

        Only messages a bot should act on come out: replayed history is dropped,
        prompts are answered and swallowed, and proxy-level notices are logged.
        """
        attempt = 0
        while not self._stopped:
            try:
                backfill = await self._establish()
                attempt = 0

                for msg in backfill:
                    out = await self._handle(msg)
                    if out is not None:
                        yield out

                assert self.client is not None
                async for msg in self.client.stream():
                    out = await self._handle(msg)
                    if out is not None:
                        yield out

                raise Disconnected("proxy closed the websocket")

            except (AuthError, NameUnavailable):
                # Neither fixes itself on a retry: bad credentials just burn the
                # proxy's per-IP failure budget into a lockout, and a name that
                # is taken stays taken. Running on under the wrong name would
                # leave the bot unaddressable, so stop instead.
                raise
            except asyncio.CancelledError:
                raise
            except Exception as e:  # noqa: BLE001 - any link failure is retryable
                if self._stopped:
                    return
                attempt += 1
                delay = self._backoff(attempt, e)
                log.warning("session lost (%s); reconnecting in %.1fs", e, delay)
                await self._teardown()
                await asyncio.sleep(delay)

    def _backoff(self, attempt: int, err: Exception) -> float:
        if isinstance(err, TransientAuthError) and err.retry_after:
            return min(err.retry_after, self.max_backoff)
        base = min(2.0 ** (attempt - 1), self.max_backoff)
        return base * (0.5 + random.random() * 0.5)  # jitter, to avoid lockstep

    async def stop(self) -> None:
        self._stopped = True
        await self._teardown()
        await self.runner.stop()

    # ── establishing a session ───────────────────────────────────────────────

    async def _establish(self) -> list[ServerMessage]:
        """Bring the session up. Returns any history missed while away."""
        handle = await self.runner.ensure_running()

        if self.client is None or self.client.base_url != handle.base_url:
            await self._close_client()
            self.client = ZlilyClient(handle.base_url)

        token = await self.client.auth(self.username, self.password)
        # A live session hands back the same token and keeps counting message
        # ids; a fresh one restarts them at 1, which would make our highwater
        # swallow everything new.
        new_session = token != self._token
        if new_session:
            log.info("new session for %s", self.username)
            self._token = token
            self._highwater = 0
            self._reported_seen = 0
            self._answered_prompts.clear()

        backfill: list[ServerMessage] = []
        buffered_prompt: ServerMessage | None = None
        if new_session:
            # Jump to the end of the ring buffer before connecting: on a fresh
            # session the proxy replays its whole history over the socket, and a
            # bot that answered all of it would relitigate a day of
            # conversation. /events does not wait on the sync, so this is safe
            # to ask for first.
            self._highwater, buffered_prompt = await self.client.scan_history()
            log.info("skipping replay up to event %d", self._highwater)
        elif self._highwater:
            backfill = await self._fetch_backfill()

        # The socket has to be open and draining *before* /state: that request
        # blocks until the SLCP sync completes, and a prompt pending at login
        # gates the sync. Answering it is what lets /state return, so a client
        # that waits for /state before reading deadlocks against its own prompt.
        await self.client.connect_ws()

        # A prompt that predates the socket reaches us only in the replay the
        # highwater is discarding, so answer it from the history scan.
        if buffered_prompt is not None:
            await self._answer_prompt(buffered_prompt.id, buffered_prompt.as_text)

        early: list[ServerMessage] = []
        state = await self._state_while_reading(early)

        self.state = state
        self.whoami = state.whoami
        self.entities.replace_all(state.entities)
        if new_session:
            self._highwater = max(self._highwater, state.last_seen_id)

        await self._ensure_name()
        await self._answer_snapshot_prompt(state)

        for disc in self.join:
            await self.send(f"/join {disc}")

        self._start_seen_reporter()
        if self.on_ready is not None:
            await self.on_ready(self)
        log.info("connected as %s (%s) on %s",
                 self.my_name or self.username, self.whoami, state.server)
        return backfill + early

    async def _state_while_reading(self, out: list[ServerMessage]) -> State:
        """Fetch /state while draining the socket underneath it.

        Anything that arrives meanwhile is collected into `out` for the caller
        to replay, so nothing is lost; prompts are answered by _handle as they
        come, which is what unblocks the request.
        """
        assert self.client is not None
        reader = asyncio.create_task(self._drain_into(out))
        try:
            return await self.client.state()
        finally:
            reader.cancel()
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await reader

    async def _drain_into(self, out: list[ServerMessage]) -> None:
        assert self.client is not None
        async for msg in self.client.stream():
            handled = await self._handle(msg)
            if handled is not None:
                out.append(handled)

    async def _fetch_backfill(self) -> list[ServerMessage]:
        """Collect what arrived while we were disconnected.

        The gap is normally seconds, and missing a question asked during a blip
        is worse than answering it a moment late.
        """
        assert self.client is not None
        out: list[ServerMessage] = []
        after = self._highwater
        while True:
            msgs, more = await self.client.events(after=after)
            if not msgs:
                break
            out.extend(msgs)
            after = msgs[-1].id
            if not more:
                break
        if out:
            log.info("backfilling %d message(s) missed while disconnected", len(out))
        return out

    async def _ensure_name(self) -> None:
        """Claim the configured name, or refuse to run under the wrong one.

        Addressing depends on the bot's name, so running under a different one
        would leave it silently unresponsive — better to fail loudly at startup.
        """
        if not self.desired_name:
            return
        current = self.my_name
        if current and same_name(current, self.desired_name):
            return

        want = to_lily_name(self.desired_name)
        log.info("renaming from %r to %r", current or "?", want)
        await self.send(f"/rename {want}")

        assert self.client is not None
        deadline = time.monotonic() + RENAME_TIMEOUT
        while time.monotonic() < deadline:
            await asyncio.sleep(RENAME_POLL)
            state = await self.client.state()
            self.entities.replace_all(state.entities)
            if same_name(self.my_name, self.desired_name):
                log.info("now named %s", self.my_name)
                return

        raise NameUnavailable(
            f"could not rename to {self.desired_name!r}: the server still reports "
            f"{self.my_name or 'no name'} after {RENAME_TIMEOUT:.0f}s. The name is "
            "probably taken or not permitted; pick another, or drop bot.name to "
            "keep the account's current name."
        )

    async def _answer_snapshot_prompt(self, state: State) -> None:
        """Clear a prompt that was already pending when we logged in."""
        if state.prompt and state.prompt_id is not None:
            await self._answer_prompt(state.prompt_id, state.prompt)

    # ── inbound ──────────────────────────────────────────────────────────────

    async def _handle(self, msg: ServerMessage) -> ServerMessage | None:
        """Apply session-level bookkeeping. Returns None to swallow a message."""
        if msg.type == "error":
            text = msg.as_text
            if text == LILY_CLOSED:
                # The proxy deletes the session and closes the socket next; our
                # token is dead, so force a full re-login.
                raise Disconnected(text)
            log.warning("proxy: %s", text)
            return None

        if msg.id and msg.id <= self._highwater:
            return None
        if msg.id:
            self._highwater = msg.id

        if msg.type == "prompt":
            await self._answer_prompt(msg.id, msg.as_text)
            return None

        if msg.type == "commandresult" and self._claim_command_result(msg):
            return None

        if (ev := msg.event) is not None:
            self.entities.apply(ev)
        elif msg.type in ("text", "input", "commandresult", "clientcommand"):
            log.debug("proxy %s: %.200s", msg.type, msg.as_text or msg.data)

        return msg

    def _claim_command_result(self, msg: ServerMessage) -> bool:
        """Hand a command's output to a waiting run_command, if there is one."""
        waiter = self._command_result
        if waiter is None or waiter.done():
            return False
        lines = msg.data.get("lines", []) if isinstance(msg.data, dict) else []
        waiter.set_result(list(lines))
        return True

    async def _answer_prompt(self, prompt_id: int, text: str) -> None:
        """Hit enter at a Lily prompt.

        An empty line is a valid send that the proxy forwards verbatim, which is
        what clears the pending prompt server-side.
        """
        if prompt_id in self._answered_prompts:
            return
        self._answered_prompts.add(prompt_id)

        now = time.monotonic()
        self._prompt_times = [t for t in self._prompt_times if now - t < PROMPT_BURST_WINDOW]
        self._prompt_times.append(now)
        if len(self._prompt_times) > PROMPT_BURST:
            log.warning(
                "answered %d prompts in %.0fs — the server may be looping on %r",
                len(self._prompt_times), PROMPT_BURST_WINDOW, text,
            )

        log.info("skipping prompt: %s", text.strip() or "(empty)")
        await self.send("")

    # ── read position ────────────────────────────────────────────────────────

    def _start_seen_reporter(self) -> None:
        if self._seen_task is None or self._seen_task.done():
            self._seen_task = asyncio.create_task(self._report_seen())

    async def _report_seen(self) -> None:
        """Record our read position so a restart resumes instead of replaying."""
        try:
            while not self._stopped:
                await asyncio.sleep(SEEN_INTERVAL)
                if self.client is None or self._highwater <= self._reported_seen:
                    continue
                try:
                    await self.client.mark_seen(self._highwater)
                    self._reported_seen = self._highwater
                except Exception as e:  # noqa: BLE001 - best effort only
                    log.debug("could not report read position: %s", e)
        except asyncio.CancelledError:
            pass

    # ── teardown ─────────────────────────────────────────────────────────────

    async def _teardown(self) -> None:
        if self._seen_task is not None:
            self._seen_task.cancel()
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await self._seen_task
            self._seen_task = None
        if self.client is not None:
            await self.client.close_ws()

    async def _close_client(self) -> None:
        if self.client is not None:
            await self.client.aclose()
            self.client = None
