"""Handle→entity bookkeeping and Lily destination formatting."""

from __future__ import annotations

from typing import Iterable

from .events import Entity, Event


def to_lily_name(name: str) -> str:
    """Lily matches names with underscores where the display name has spaces."""
    return name.replace(" ", "_")


def same_name(a: str, b: str) -> bool:
    """Compare two names the way Lily does — spaces and case are not significant."""
    return to_lily_name(a).casefold() == to_lily_name(b).casefold()


class Entities:
    """The client's view of who and what exists on the server.

    Seeded from `GET /state`, then kept current from the `entities` snapshot
    embedded in every event plus the events that change a name.
    """

    def __init__(self) -> None:
        self._by_handle: dict[str, Entity] = {}

    def replace_all(self, entities: Iterable[Entity]) -> None:
        self._by_handle = {e.handle: e for e in entities if e.handle}

    def get(self, handle: str) -> Entity | None:
        return self._by_handle.get(handle)

    def name(self, handle: str) -> str:
        e = self._by_handle.get(handle)
        return e.name if e and e.name else handle

    def nick(self, handle: str) -> str:
        """The sender name in the underscored form infobot's patterns expect."""
        return to_lily_name(self.name(handle))

    def dest(self, handle: str) -> str:
        """Format a handle as a Lily send destination.

        Discussions take a leading `-`; both kinds have spaces underscored. This
        matches the browser client (web/src/components/Compose.svelte:11-17).
        """
        e = self._by_handle.get(handle)
        if e is None:
            return handle
        name = to_lily_name(e.name) if e.name else handle
        return f"-{name}" if e.kind == "disc" else name

    def dest_list(self, handles: Iterable[str]) -> str:
        """Comma-joined destinations — Lily's syntax for a multi-way send."""
        return ",".join(self.dest(h) for h in handles)

    def is_emote_dest(self, handles: Iterable[str]) -> bool:
        """True when any destination is an emote-only discussion."""
        return any(
            (e := self._by_handle.get(h)) is not None and e.is_emote_disc
            for h in handles
        )

    def apply(self, ev: Event) -> None:
        """Fold an event's entity snapshot, and any rename it carries, into the table."""
        for handle, ent in ev.entities.items():
            self._merge(handle, ent)

        # `rename` and `drename` carry the new name in `value`; the embedded
        # snapshot is taken before the change, so apply it afterwards.
        if ev.event in ("rename", "drename") and ev.source and ev.value:
            cur = self._by_handle.get(ev.source)
            if cur is not None:
                self._by_handle[ev.source] = _replace(cur, name=ev.value)

    def _merge(self, handle: str, incoming: Entity) -> None:
        """Update a handle without letting omitted optional fields blank it out.

        The per-event snapshot carries only the fields the proxy filled in, so a
        blind overwrite would drop a blurb or title we already knew.
        """
        cur = self._by_handle.get(handle)
        if cur is None:
            self._by_handle[handle] = incoming
            return
        merged = {}
        for f in (
            "kind", "name", "blurb", "state", "pronoun",
            "title", "attrib", "creation", "members",
        ):
            new = getattr(incoming, f)
            merged[f] = new if new else getattr(cur, f)
        # `member` is a real boolean: false is meaningful, not "unset".
        merged["member"] = incoming.member
        self._by_handle[handle] = _replace(cur, **merged)


def _replace(e: Entity, **changes) -> Entity:
    from dataclasses import replace

    return replace(e, **changes)
