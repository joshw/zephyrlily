"""The Bot base class: dispatch Lily messages, and reply to the right place."""

from __future__ import annotations

import asyncio
import contextlib
import logging
from typing import Coroutine, Sequence

from .entities import Entities, same_name
from .events import Event, Message, ServerMessage, unwrap_emote
from .session import Session

log = logging.getLogger(__name__)

# Owners address the bot's own controls with this prefix, privately, so an
# admin command can never be mistaken for something said in a discussion.
ADMIN_PREFIX = "$"

# The commands the default on_admin exposes, as prefix and argument summary.
# Arguments are passed through verbatim: `/ignore` takes an optional type
# ("/ignore bob all"), so collapsing the argument to a single name would break
# it. An empty prefix means the argument *is* the whole line, which is what
# makes `$cmd` a general escape hatch.
ADMIN_COMMANDS = {
    "cmd": ("", "<lily command>"),
    "ignore": ("/ignore", "<person>"),
    "join": ("/join", "<discussion>"),
    "quit": ("/quit", "<discussion>"),
    "unignore": ("/unignore", "<person>"),
}

# A command's output is relayed line by line, since a Lily send is one line.
MAX_ADMIN_LINES = 20


class Bot:
    """Subclass this and override the handlers you care about.

    The run loop drops our own sends before dispatch, so a bot cannot answer
    itself into a loop.
    """

    def __init__(self, session: Session, *, owners: Sequence[str] = ()) -> None:
        self.session = session
        self.owners = tuple(owners)
        self._tasks: set[asyncio.Task] = set()

    @property
    def entities(self) -> Entities:
        return self.session.entities

    @property
    def whoami(self) -> str:
        return self.session.whoami

    @property
    def my_name(self) -> str:
        return self.session.my_name

    # ── handlers to override ─────────────────────────────────────────────────

    async def on_public(self, m: Message) -> None: ...

    async def on_private(self, m: Message) -> None: ...

    async def on_emote(self, m: Message) -> None: ...

    async def on_event(self, e: Event) -> None:
        """Every non-message event: presence, renames, joins, and so on."""

    async def on_own_message(self, m: Message) -> None:
        """Our own send, echoed back. Ignored by default."""

    async def on_admin(self, m: Message, command: str, args: str) -> None:
        """A `$command` sent privately by a configured owner.

        The default implementation runs the corresponding Lily command and
        relays its output back. Override it to add commands of your own --
        calling `super().on_admin(...)` keeps these.
        """
        dest = self.entities.dest(m.sender)

        entry = ADMIN_COMMANDS.get(command)
        if entry is None:
            known = ", ".join(f"${c}" for c in sorted(ADMIN_COMMANDS))
            await self.say(dest, f"unknown command ${command}. known: {known}")
            return

        prefix, usage = entry
        if not args:
            await self.say(dest, f"usage: ${command} {usage}")
            return

        line = f"{prefix} {args}".strip()
        log.info("admin: %s ran %s", m.nick, line)
        lines = await self.session.run_command(line)

        if not lines:
            await self.say(dest, f"{line}: sent, no output")
            return
        for text in lines[:MAX_ADMIN_LINES]:
            await self.say(dest, text)
        if len(lines) > MAX_ADMIN_LINES:
            await self.say(dest, f"... {len(lines) - MAX_ADMIN_LINES} more lines")

    # ── owners ───────────────────────────────────────────────────────────────

    def is_owner(self, m: Message) -> bool:
        """Owners are matched by Lily name, spacing and case insensitive."""
        return any(same_name(m.nick, owner) for owner in self.owners)

    def parse_admin(self, m: Message) -> tuple[str, str] | None:
        """Split a private owner message into a `$command` and its arguments."""
        if m.is_public or not self.is_owner(m):
            return None
        body = m.body.strip()
        if not body.startswith(ADMIN_PREFIX):
            return None
        command, _, args = body[len(ADMIN_PREFIX):].strip().partition(" ")
        return command.lower(), args.strip()

    # ── sending ──────────────────────────────────────────────────────────────

    async def say(self, dest: str, text: str) -> None:
        await self.session.say(dest, text)

    async def emote(self, dest: str, text: str) -> None:
        await self.session.emote(dest, text)

    async def reply(self, m: Message, text: str, *, action: bool = False) -> None:
        """Answer a message where its sender will see it.

        Public and emote messages go back to the discussion; private ones go
        back to the sender alone.
        """
        dest = m.dest_str if m.is_public else self.entities.dest(m.sender)
        if not dest:
            log.warning("no destination for a reply to %s; dropping it", m.sender)
            return
        await self._send_to(dest, text, action=action, emote_context=self._emote_context(m))

    async def _send_to(self, dest: str, text: str, *, action: bool, emote_context: bool) -> None:
        """Send text into a destination, respecting emote-only discussions.

        In a discussion where every send renders as an emote, plain speech needs
        a leading `"` to read as speech, and an action needs no prefix at all
        because it is already in the right voice. Outside one, an action has to
        be sent as an explicit emote.
        """
        if emote_context:
            await self.say(dest, text if action else f'"{text}"')
        elif action:
            await self.emote(dest, text)
        else:
            await self.say(dest, text)

    def _emote_context(self, m: Message) -> bool:
        if not m.is_public:
            return False
        return self.entities.is_emote_dest(m.dests) or m.is_emote

    # ── the loop ─────────────────────────────────────────────────────────────

    async def run(self) -> None:
        try:
            async for msg in self.session.messages():
                try:
                    await self._dispatch(msg)
                except Exception:  # noqa: BLE001 - one bad message must not kill the bot
                    log.exception("handler failed for message %d (%s)", msg.id, msg.type)
        finally:
            await self._drain_tasks()

    def spawn(self, coro: Coroutine) -> asyncio.Task:
        """Run a handler off the message loop.

        Anything that waits for Lily to answer -- `Session.run_command` above
        all -- must not run inline: the reply it is waiting for arrives on the
        very loop the handler is standing in, so awaiting it there deadlocks.
        """
        task = asyncio.create_task(coro)
        self._tasks.add(task)
        task.add_done_callback(self._tasks.discard)
        task.add_done_callback(_log_task_failure)
        return task

    async def _drain_tasks(self) -> None:
        for task in list(self._tasks):
            task.cancel()
        for task in list(self._tasks):
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await task

    async def _dispatch(self, msg: ServerMessage) -> None:
        ev = msg.event
        if ev is None:
            return

        if ev.event not in ("public", "private", "emote"):
            await self.on_event(ev)
            return

        m = self.to_message(ev)
        if ev.source and ev.source == self.whoami:
            await self.on_own_message(m)
            return

        if (admin := self.parse_admin(m)) is not None:
            self.spawn(self.on_admin(m, *admin))
            return

        if ev.event == "public":
            await self.on_public(m)
        elif ev.event == "private":
            await self.on_private(m)
        else:
            await self.on_emote(m)

    def to_message(self, ev: Event) -> Message:
        nick = self.entities.nick(ev.source)
        is_emote = ev.event == "emote"
        body = unwrap_emote(nick, ev.value) if is_emote else ev.value
        return Message(
            kind=ev.event,
            sender=ev.source,
            nick=nick,
            dests=ev.recips,
            dest_str=self.entities.dest_list(ev.recips),
            body=body,
            raw_body=ev.value,
            is_emote=is_emote,
            time=ev.time,
            event=ev,
        )


def _log_task_failure(task: asyncio.Task) -> None:
    if task.cancelled():
        return
    if (exc := task.exception()) is not None:
        log.error("background handler failed: %s", exc, exc_info=exc)
