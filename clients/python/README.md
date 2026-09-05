# zlilybot

Write [Lily](https://en.wikipedia.org/wiki/Lily_(chat)) chat bots in Python,
against a [zlily](../../README.md) proxy.

The proxy does the hard part — it speaks SLCP to the Lily server, keeps the
entity database, and exposes a JSON HTTP + WebSocket API. `zlilybot` is the
Python side of that API, plus the operational scaffolding a bot needs to stay
online unattended.

```python
import asyncio
from zlilybot import Bot, ProxyRunner, Session

class EchoBot(Bot):
    async def on_public(self, m):
        if m.body.startswith("echo "):
            await self.reply(m, m.body[5:])

async def main():
    session = Session(
        ProxyRunner(mode="spawn"),      # or mode="attach", url="..."
        "myuser", "mypassword",
        name="EchoBot",                 # optional: claim this name on Lily
        join=["-test"],
    )
    await EchoBot(session).run()

asyncio.run(main())
```

## What it handles for you

**Getting a proxy.** `ProxyRunner(mode="spawn")` starts `zlily server` on a port
it picks, waits for it to answer, and reaps it on exit; `mode="attach"` connects
to one already running. The binary is `--zlily` → `zlily_path` → `PATH`, and a
configured path that does not resolve is a fatal error rather than a silent
fallback to a different build.

**Staying logged in.** `Session.messages()` is one async iterator that survives
the link dropping. It re-authenticates, backfills what it missed, and knows that
a dead session mints a new token and restarts message ids at 1.

**The startup replay.** A fresh session is sent the proxy's whole ring buffer. A
bot that answered it would relitigate a day of conversation, so history is
skipped up to a highwater established before the socket opens.

**Prompts.** Lily can hold a login behind a prompt, and `GET /state` blocks on
the sync that prompt gates — so a client that waits for `/state` before reading
deadlocks against a prompt only it can clear. The session opens the socket
first, answers each prompt once with an empty line, and never re-answers one
replayed from history.

**Names.** With `name=` set, the session renames itself if it has to and refuses
to start if the name is unavailable, rather than running unaddressable.

**Owner commands.** `Bot(session, owners=[...])` enables `$` commands sent
privately by those users: `$join`, `$quit`, `$ignore`, `$unignore`, and `$cmd`
for anything else. Output is relayed back. Override `on_admin` to add your own
and call `super().on_admin(...)` to keep these.

## Handlers

| | |
|---|---|
| `on_public(m)` | a send to a discussion |
| `on_private(m)` | a send to you |
| `on_emote(m)` | an emote, already unwrapped |
| `on_event(e)` | everything else: joins, renames, presence |
| `on_admin(m, command, args)` | a `$` command from an owner |
| `on_own_message(m)` | your own send, echoed back (ignored by default) |

`reply(m, text)` answers where the sender will see it — the discussion for a
public message, the sender alone for a private one — and handles emote-only
discussions, where plain speech needs a leading quote.

## Stored content, names and links

A session also wraps the proxy endpoints beyond the message stream:

```python
sayings = await session.fetch("memo", "cj-admin", "sayings")   # GET /fetch
await session.store(lines, "memo", "cj-admin", "sayings")      # POST /store
matches = await session.expand("emac")                          # GET /expand
short   = await session.shorten(long_url)                       # POST /shorten
```

`fetch`/`store` read and write a user's `/info` or a named memo, which is a
convenient place to keep a bot's data where anyone with lily access can edit it.
`shorten` uses whatever service the proxy was built with, so a bot inherits its
API key and has nothing to configure.

## Running Lily commands

`await session.run_command("/who bob")` sends a command and returns the lines it
printed. Lily assigns the id that frames command output, so there is nothing a
client can set to correlate a reply; this claims the next result, which is sound
because commands are serialised and a bot has its own session.

**It must not be awaited inline in a handler.** The reply arrives on the message
loop the handler is standing in, so awaiting it there deadlocks. Use
`Bot.spawn(coro)` to run that work off the loop — `on_admin` is already
dispatched that way.

## Testing a bot

`zlilybot.testing.FakeProxy` is an in-process proxy speaking the real wire
format, so a bot can be driven with no Lily server and no proxy binary:

```python
from zlilybot.testing import FakeProxy

async with FakeProxy() as proxy:
    session = Session(ProxyRunner(mode="attach", url=proxy.base_url), "bot", "pw")
    task = asyncio.create_task(EchoBot(session).run())
    proxy.push_event("public", "#2", recips=["#10"], value="echo hello")
    ...
    assert "-test;hello" in proxy.received
```

`push_event`, `push_prompt`, `push_error` and `kill_session` script the proxy;
`received` records what the bot sent. `state_gated_on_prompt` and
`rename_should_fail` reproduce the awkward login cases.

## Encoding

SLCP carries 7-bit ASCII, but the proxy folds every outbound line in
`Conn.Send`, so you can send text as typed.

## Install

```sh
pip install zlilybot
```

Or from this repository:

```sh
pip install "git+https://github.com/joshw/zephyrlily#subdirectory=clients/python"
```

Requires Python 3.11+. To let a bot spawn its own proxy you also need the
`zlily` binary; attaching to a running one needs nothing extra.

## Why it lives here

`zlilybot` is defined entirely by the proxy's behaviour — every subtlety above
is a fact about `internal/proxy/api`, not about Python. Keeping it in the same
repository means a change to the API and the client that consumes it land
together and are tested in the same CI run. See [docs/client-api.md](../../docs/client-api.md)
for the API it wraps.
