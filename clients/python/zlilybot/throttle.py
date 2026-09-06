"""Per-sender rate limiting.

Protection against one person flooding a bot. Keyed by sender rather than by
destination on purpose: budgeting per discussion means a flooder exhausts it
and the bot falls silent for everyone else in the room, which punishes the
wrong people.

Someone sending faster than `interval` repeatedly is first put on notice, and
if they keep going the caller is told it may escalate -- typically to Lily's
own `/ignore`. What to do with that verdict is the bot's decision; this only
reports it.

    throttle = Throttle()
    verdict = throttle.check(message.nick)
    if verdict is Verdict.IGNORE:
        await session.send(f"/ignore {nick} all")
    if verdict is not Verdict.OK:
        return          # WARN and SILENT both mean "say nothing"
"""

from __future__ import annotations

import logging
import time
from dataclasses import dataclass, field
from enum import Enum

log = logging.getLogger(__name__)


class Verdict(Enum):
    OK = "ok"
    WARN = "warn"      # tripped the limit for the first time
    IGNORE = "ignore"  # tripped it again; caller may ignore them at the server
    SILENT = "silent"  # already on notice; say nothing


@dataclass
class _Sender:
    last: float = 0.0
    count: int = 0
    flagged: bool = False


@dataclass
class Throttle:
    interval: float = 1.0
    safety: float = 5.0
    burst: int = 3
    senders: dict[str, _Sender] = field(default_factory=dict)

    def check(self, who: str, now: float | None = None) -> Verdict:
        now = time.monotonic() if now is None else now
        s = self.senders.setdefault(who, _Sender())
        gap, s.last = now - s.last, now

        if gap < self.interval:
            s.count += 1
        elif gap > self.safety:
            # A quiet spell clears the record entirely, so someone who was
            # merely enthusiastic is not held to it forever.
            s.count, s.flagged = 0, False

        if s.count > self.burst:
            s.count = 0
            if s.flagged:
                log.warning("%s tripped the throttle again", who)
                return Verdict.IGNORE
            s.flagged = True
            log.info("%s tripped the throttle", who)
            return Verdict.WARN

        return Verdict.SILENT if s.flagged else Verdict.OK
