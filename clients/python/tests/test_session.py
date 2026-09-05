"""Session lifecycle: logging in, staying logged in, and the surprises in the
proxy's behaviour that a client has to be built around."""

from __future__ import annotations

import asyncio

import pytest

from zlilybot import Bot, Message, ProxyRunner, Session
from zlilybot.errors import AuthError, NameUnavailable
from zlilybot.testing import FakeProxy, FakeSession

BOT = "#1"
ALICE = "#2"
DISC = "#10"


class RecordingBot(Bot):
    """Echoes public messages and remembers everything it was handed."""

    def __init__(self, session, **kw):
        super().__init__(session, **kw)
        self.seen: list[Message] = []
        self.events: list[str] = []

    async def on_public(self, m: Message) -> None:
        self.seen.append(m)
        await self.reply(m, f"heard: {m.body}")

    async def on_private(self, m: Message) -> None:
        self.seen.append(m)

    async def on_event(self, e) -> None:
        self.events.append(e.event)


class Harness:
    def __init__(self, proxy: FakeProxy, *, name=None, join=(), owners=()):
        self.proxy = proxy
        self.session = Session(
            ProxyRunner(mode="attach", url=proxy.base_url),
            "bot", "pw", name=name, join=join,
        )
        self.ready = asyncio.Event()
        original = self.session.on_ready

        async def on_ready(s):
            if original:
                await original(s)
            self.ready.set()

        self.session.on_ready = on_ready
        self.bot = RecordingBot(self.session, owners=owners)
        self.task: asyncio.Task | None = None
        self.cursor = 0

    async def start(self):
        self.task = asyncio.create_task(self.bot.run())
        waiter = asyncio.create_task(self.ready.wait())
        done, _ = await asyncio.wait(
            {waiter, self.task}, timeout=20, return_when=asyncio.FIRST_COMPLETED
        )
        waiter.cancel()
        if self.task in done:
            self.task.result()
            raise AssertionError("the bot exited during startup")
        assert self.ready.is_set(), "the bot never connected"

    async def stop(self):
        if self.task:
            self.task.cancel()
            try:
                await self.task
            except (asyncio.CancelledError, Exception):
                pass
        await self.session.stop()

    def push(self, event, text, *, source=ALICE, recips=None):
        self.cursor = len(self.proxy.received)
        self.proxy.push_event(event, source, recips=recips or [DISC], value=text)

    async def next_line(self, timeout=3.0) -> str:
        async def wait():
            while len(self.proxy.received) <= self.cursor:
                await asyncio.sleep(0.005)
            line = self.proxy.received[self.cursor]
            self.cursor += 1
            return line

        return await asyncio.wait_for(wait(), timeout)

    async def expect_silence(self, seconds=0.25):
        before = len(self.proxy.received)
        await asyncio.sleep(seconds)
        assert len(self.proxy.received) == before, self.proxy.received[before:]


@pytest.fixture
async def harness():
    proxy = FakeProxy()
    await proxy.start()
    h = Harness(proxy)
    await h.start()
    yield h
    await h.stop()
    await proxy.stop()


# ── the basics ───────────────────────────────────────────────────────────────


async def test_messages_reach_the_bot_and_replies_reach_the_discussion(harness):
    harness.push("public", "hello")
    assert await harness.next_line() == "-test;heard: hello"


async def test_our_own_sends_are_not_dispatched_back_to_us(harness):
    """Otherwise the first reply starts a loop."""
    harness.proxy.push_event("public", BOT, recips=[DISC], value="heard: hello")
    await harness.expect_silence()


async def test_unicode_is_left_for_the_proxy_to_fold(harness):
    """Conn.Send folds to ASCII, so a client sends text as typed."""
    harness.push("public", "café")
    assert "café" in await harness.next_line()


# ── the startup replay ───────────────────────────────────────────────────────


async def test_a_fresh_session_does_not_answer_the_backlog():
    """The proxy replays its whole ring buffer to a client that has never
    reported a read position; answering it would relitigate a day of talk."""
    proxy = FakeProxy()
    await proxy.start()
    seeded = FakeSession(token="seed", username="bot")
    proxy.sessions["seed"] = seeded
    proxy.session_by_user["bot"] = seeded
    for i in range(5):
        proxy.buffer_event("public", ALICE, recips=[DISC], value=f"old message {i}")

    h = Harness(proxy)
    await h.start()
    try:
        await h.expect_silence(0.4)
        assert h.bot.seen == []
    finally:
        await h.stop()
        await proxy.stop()


# ── prompts ──────────────────────────────────────────────────────────────────


async def test_a_prompt_is_answered_with_an_empty_line(harness):
    harness.cursor = len(harness.proxy.received)
    harness.proxy.push_prompt("You were detached, review now? (Y/n)")
    assert await harness.next_line() == ""


async def test_a_prompt_is_answered_only_once(harness):
    harness.cursor = len(harness.proxy.received)
    pid = harness.proxy.push_prompt("Review? (Y/n)")
    assert await harness.next_line() == ""
    await harness.session._answer_prompt(pid, "Review? (Y/n)")
    await harness.expect_silence()


@pytest.mark.parametrize("when", ["buffered", "on_connect"])
async def test_login_completes_when_a_prompt_gates_the_sync(when):
    """GET /state blocks until the SLCP sync finishes, and a prompt pending at
    login gates that sync. Answering the prompt is what lets /state return, so
    the socket has to be draining first -- otherwise the client waits forever
    on a prompt only it can clear. Both arrival orderings have to work."""
    proxy = FakeProxy()
    proxy.state_gated_on_prompt = True
    await proxy.start()

    if when == "buffered":
        seeded = FakeSession(token="seed", username="bot")
        proxy.sessions["seed"] = seeded
        proxy.session_by_user["bot"] = seeded
        seeded.prompt_id = seeded.next_id
        seeded.prompt = "review now? (Y/n)"
        seeded.buffer.append(
            {"id": seeded.next_id, "type": "prompt", "data": seeded.prompt}
        )
        seeded.next_id += 1
    else:
        proxy.prompt_on_ws_connect = "Enter a blurb:"

    h = Harness(proxy)
    try:
        await h.start()
        assert "" in proxy.received, "the prompt was never answered"
        h.push("public", "hello")
        assert await h.next_line() == "-test;heard: hello"
    finally:
        await h.stop()
        await proxy.stop()


# ── credentials and names ────────────────────────────────────────────────────


async def test_bad_credentials_fail_fast_without_retrying():
    """Retrying a rejected password burns the proxy's per-IP failure budget."""
    proxy = FakeProxy(users={"bot": "right"})
    await proxy.start()
    session = Session(ProxyRunner(mode="attach", url=proxy.base_url), "bot", "wrong")
    bot = RecordingBot(session)
    try:
        with pytest.raises(AuthError):
            await asyncio.wait_for(bot.run(), timeout=3)
        assert proxy.auth_calls == 1
    finally:
        await session.stop()
        await proxy.stop()


async def test_a_matching_name_needs_no_rename():
    proxy = FakeProxy()
    proxy.set_name(BOT, "Echo")
    await proxy.start()
    h = Harness(proxy, name="Echo")
    await h.start()
    try:
        assert not any(l.startswith("/rename") for l in proxy.received)
    finally:
        await h.stop()
        await proxy.stop()


async def test_a_different_name_is_claimed():
    proxy = FakeProxy()
    await proxy.start()
    h = Harness(proxy, name="Echo")
    await h.start()
    try:
        assert "/rename Echo" in proxy.received
        assert h.session.my_name == "Echo"
    finally:
        await h.stop()
        await proxy.stop()


async def test_an_unavailable_name_aborts_rather_than_running_unaddressable():
    proxy = FakeProxy()
    proxy.rename_should_fail = True
    await proxy.start()
    h = Harness(proxy, name="Taken")
    try:
        with pytest.raises(NameUnavailable, match="Taken"):
            await asyncio.wait_for(h.bot.run(), timeout=20)
    finally:
        await h.stop()
        await proxy.stop()


async def test_configured_discussions_are_joined():
    proxy = FakeProxy()
    await proxy.start()
    h = Harness(proxy, join=["-test", "-bots"])
    await h.start()
    try:
        assert "/join -test" in proxy.received
        assert "/join -bots" in proxy.received
    finally:
        await h.stop()
        await proxy.stop()


# ── staying connected ────────────────────────────────────────────────────────


async def test_reconnects_after_the_lily_link_drops():
    proxy = FakeProxy()
    await proxy.start()
    h = Harness(proxy)
    await h.start()
    try:
        before = proxy.auth_calls
        proxy.kill_session()
        for _ in range(400):
            if proxy.auth_calls > before and proxy.sessions:
                break
            await asyncio.sleep(0.01)
        assert proxy.auth_calls > before
        await asyncio.sleep(0.15)

        # A new session restarts message ids at 1; the highwater must not
        # swallow everything that follows.
        h.push("public", "still here")
        assert await h.next_line() == "-test;heard: still here"
    finally:
        await h.stop()
        await proxy.stop()


async def test_the_read_position_is_reported():
    proxy = FakeProxy()
    await proxy.start()
    h = Harness(proxy)
    await h.start()
    try:
        h.push("public", "hello")
        await h.next_line()
        for _ in range(700):
            if proxy.seen_calls:
                break
            await asyncio.sleep(0.01)
        assert proxy.seen_calls and proxy.seen_calls[-1] > 0
    finally:
        await h.stop()
        await proxy.stop()


# ── running Lily commands ────────────────────────────────────────────────────


async def test_run_command_collects_the_output(harness):
    harness.proxy.command_output["/who Bob"] = ["Bob   here   3m"]
    assert await harness.session.run_command("/who Bob") == ["Bob   here   3m"]


async def test_run_command_returns_empty_when_nothing_is_framed(harness):
    """Not every Lily command leafs its output, so this is not a failure."""
    assert await harness.session.run_command("/nothing", timeout=0.5) == []


async def test_run_command_does_not_deadlock_inside_a_handler():
    """The reply arrives on the message loop the handler is standing in, so
    Bot.spawn has to take the work off that loop."""
    proxy = FakeProxy()
    await proxy.start()
    proxy.command_output["/who Bob"] = ["Bob   here"]

    class Asker(RecordingBot):
        async def on_public(self, m):
            self.spawn(self._ask(m))

        async def _ask(self, m):
            lines = await self.session.run_command("/who Bob")
            await self.reply(m, " ".join(lines))

    h = Harness(proxy)
    h.bot = Asker(h.session)
    await h.start()
    try:
        h.push("public", "who is around?")
        assert await h.next_line() == "/who Bob"
        assert await h.next_line() == "-test;Bob   here"
    finally:
        await h.stop()
        await proxy.stop()


# ── owner commands ───────────────────────────────────────────────────────────


async def test_owners_can_run_admin_commands():
    proxy = FakeProxy()
    await proxy.start()
    proxy.command_output["/join -lounge"] = ["(joining lounge)"]
    h = Harness(proxy, owners=["Alice"])
    await h.start()
    try:
        h.cursor = len(proxy.received)
        proxy.push_event("private", ALICE, recips=[BOT], value="$join -lounge")
        assert await h.next_line() == "/join -lounge"
        assert await h.next_line() == "Alice;(joining lounge)"
    finally:
        await h.stop()
        await proxy.stop()


async def test_a_non_owner_gets_no_admin_access():
    proxy = FakeProxy()
    await proxy.start()
    h = Harness(proxy, owners=["Alice"])
    await h.start()
    try:
        h.cursor = len(proxy.received)
        proxy.push_event("private", "#3", recips=[BOT], value="$join -lounge")
        await h.expect_silence(0.3)
    finally:
        await h.stop()
        await proxy.stop()


async def test_admin_commands_are_refused_in_public():
    """A `$` in a discussion must never be a control channel."""
    proxy = FakeProxy()
    await proxy.start()
    h = Harness(proxy, owners=["Alice"])
    await h.start()
    try:
        h.push("public", "$join -lounge")
        await h.next_line()  # the ordinary echo
        assert "/join -lounge" not in proxy.received
    finally:
        await h.stop()
        await proxy.stop()


@pytest.fixture
async def admin():
    proxy = FakeProxy()
    await proxy.start()
    h = Harness(proxy, owners=["Alice"])
    await h.start()
    yield h
    await h.stop()
    await proxy.stop()


def send_admin(h, text):
    h.cursor = len(h.proxy.received)
    h.proxy.push_event("private", ALICE, recips=[BOT], value=text)


@pytest.mark.parametrize(
    "sent,expected",
    [
        ("$join -lounge", "/join -lounge"),
        ("$quit -lounge", "/quit -lounge"),
        ("$ignore Bob", "/ignore Bob"),
        ("$unignore Bob", "/unignore Bob"),
        # Arguments pass through, so /ignore's optional type argument survives.
        ("$ignore Bob all", "/ignore Bob all"),
        # $cmd has no prefix of its own: the argument is the whole line.
        ("$cmd /me", "/me"),
        ("$cmd /who Bob", "/who Bob"),
    ],
)
async def test_admin_commands_map_to_lily_commands(admin, sent, expected):
    send_admin(admin, sent)
    assert await admin.next_line() == expected


async def test_admin_output_is_relayed_line_by_line(admin):
    admin.proxy.command_output["/ignore Bob"] = ["line one", "line two"]
    send_admin(admin, "$ignore Bob")
    await admin.next_line()
    assert await admin.next_line() == "Alice;line one"
    assert await admin.next_line() == "Alice;line two"


async def test_a_command_with_no_output_says_so(admin):
    """Not every Lily line produces a framed reply -- a send, for instance."""
    send_admin(admin, "$join -quiet")
    assert await admin.next_line() == "/join -quiet"
    assert await admin.next_line(timeout=10) == "Alice;/join -quiet: sent, no output"


async def test_an_unknown_command_lists_the_known_ones(admin):
    send_admin(admin, "$destroy everything")
    reply = await admin.next_line()
    assert reply.startswith("Alice;unknown command $destroy")
    assert "$join" in reply and "$cmd" in reply


@pytest.mark.parametrize(
    "sent,usage",
    [
        ("$join", "usage: $join <discussion>"),
        ("$ignore", "usage: $ignore <person>"),
        ("$cmd", "usage: $cmd <lily command>"),
    ],
)
async def test_a_command_without_arguments_gets_usage(admin, sent, usage):
    send_admin(admin, sent)
    assert await admin.next_line() == f"Alice;{usage}"


async def test_owner_matching_ignores_spacing_and_case():
    proxy = FakeProxy()
    proxy.set_name(ALICE, "Alice Smith")
    await proxy.start()
    h = Harness(proxy, owners=["alice_smith"])
    await h.start()
    try:
        send_admin(h, "$join -x")
        assert await h.next_line() == "/join -x"
    finally:
        await h.stop()
        await proxy.stop()


async def test_a_bot_with_no_owners_takes_no_orders(harness):
    harness.cursor = len(harness.proxy.received)
    harness.proxy.push_event("private", ALICE, recips=[BOT], value="$join -lounge")
    await harness.expect_silence(0.3)
