"""Wire types for the zlily proxy's WebSocket and HTTP payloads.

The authoritative definitions live in `internal/proxy/api/types.go`; the field
names here match its JSON tags exactly.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any

# Message events carry text a bot might answer. Everything else is presence,
# identity, discussion, or permission bookkeeping.
MESSAGE_EVENTS = frozenset({"public", "private", "emote"})


@dataclass(frozen=True)
class Entity:
    """A user, discussion, or group as the proxy reports it."""

    handle: str
    kind: str = ""  # "user" | "disc" | "group"
    name: str = ""
    blurb: str = ""
    state: str = ""  # "here" | "away"
    pronoun: str = ""
    title: str = ""
    attrib: str = ""
    creation: int = 0
    members: tuple[str, ...] = ()
    member: bool = False

    @classmethod
    def from_json(cls, d: dict[str, Any]) -> "Entity":
        return cls(
            handle=d.get("handle", ""),
            kind=d.get("kind", ""),
            name=d.get("name", ""),
            blurb=d.get("blurb", ""),
            state=d.get("state", ""),
            pronoun=d.get("pronoun", ""),
            title=d.get("title", ""),
            attrib=d.get("attrib", ""),
            creation=d.get("creation", 0) or 0,
            members=tuple(d.get("members") or ()),
            member=bool(d.get("member", False)),
        )

    @property
    def is_emote_disc(self) -> bool:
        """True for a discussion where every send renders as an emote.

        `attrib` is a multi-valued field; the browser client tests it the same
        way (web/src/components/Compose.svelte).
        """
        return "emote" in self.attrib


@dataclass(frozen=True)
class Event:
    """The `data` of a `type:"event"` WebSocket message."""

    event: str
    source: str = ""
    time: int = 0
    value: str = ""
    recips: tuple[str, ...] = ()
    targets: tuple[str, ...] = ()
    sub_evt: str = ""
    notify: bool = False
    stamp: bool = False
    entities: dict[str, Entity] = field(default_factory=dict)
    text: str = ""

    @classmethod
    def from_json(cls, d: dict[str, Any]) -> "Event":
        ents = {
            h: Entity.from_json({"handle": h, **e})
            for h, e in (d.get("entities") or {}).items()
        }
        return cls(
            event=d.get("event", ""),
            source=d.get("source", ""),
            time=d.get("time", 0) or 0,
            value=d.get("value", "") or "",
            recips=tuple(d.get("recips") or ()),
            targets=tuple(d.get("targets") or ()),
            sub_evt=d.get("sub_evt", "") or "",
            notify=bool(d.get("notify", False)),
            stamp=bool(d.get("stamp", False)),
            entities=ents,
            text=d.get("text", "") or "",
        )


@dataclass(frozen=True)
class ServerMessage:
    """One `WSServerMsg` off the wire.

    `data` is polymorphic: `event`/`text`/`input`/`commandresult`/
    `clientcommand` carry objects, while `prompt` and `error` carry a bare JSON
    string (internal/proxy/api/server.go).
    """

    id: int
    type: str
    data: Any

    @classmethod
    def from_json(cls, d: dict[str, Any]) -> "ServerMessage":
        return cls(id=d.get("id", 0) or 0, type=d.get("type", ""), data=d.get("data"))

    @property
    def event(self) -> Event | None:
        if self.type == "event" and isinstance(self.data, dict):
            return Event.from_json(self.data)
        return None

    @property
    def as_text(self) -> str:
        """The string payload, whichever shape this message type uses."""
        if isinstance(self.data, str):
            return self.data
        if isinstance(self.data, dict):
            return self.data.get("text", "") or ""
        return ""


# ── emote unwrapping ─────────────────────────────────────────────────────────
#
# Ported from reference/tigerlily/extensions/infobot.pl:61-67. Lily emote values
# begin with a space, so the fallback concatenation reads correctly.

_THOUGHT_RE = re.compile(r"^ \. o O \((.*)\)$")
_QUOTED_RE = re.compile(r'^ (?:asks|says), "(.*)"$')


def unwrap_emote(sender: str, value: str) -> str:
    """Turn an emote's raw value into the sentence the bot should read.

    ``. o O (foo)`` and ``asks, "foo"`` are the conventional ways to speak
    inside an emote-only discussion, so their contents are the real message.
    Anything else is a genuine action and reads as "<sender> <action>".
    """
    m = _THOUGHT_RE.match(value)
    if m:
        return m.group(1)
    m = _QUOTED_RE.match(value)
    if m:
        return m.group(1)
    return f"{sender}{value}"


@dataclass(frozen=True)
class Message:
    """A `public`, `private`, or `emote` event, normalized for a bot.

    `body` is the text to interpret: for an emote it has already been unwrapped.
    `nick` is the sender's name with spaces collapsed to underscores, which is
    the form infobot's addressing and person-switching patterns expect.
    """

    kind: str  # "public" | "private" | "emote"
    sender: str  # handle
    nick: str
    dests: tuple[str, ...]  # handles
    dest_str: str  # comma-joined, ready to use as a send destination
    body: str
    raw_body: str
    is_emote: bool
    time: int
    event: Event

    @property
    def is_public(self) -> bool:
        return self.kind != "private"
