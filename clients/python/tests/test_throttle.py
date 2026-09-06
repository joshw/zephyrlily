"""Rate limiting, and the escalation to a server-side ignore."""

from __future__ import annotations

from zlilybot.throttle import Throttle, Verdict


def test_ordinary_conversation_passes():
    t = Throttle()
    assert [t.check("alice", now=n) for n in (0, 2, 4, 6)] == [Verdict.OK] * 4


def test_a_burst_trips_a_warning():
    t = Throttle(interval=1.0, burst=3)
    # Four sends inside a second: the fourth is one too many.
    verdicts = [t.check("alice", now=0.1 * i) for i in range(6)]
    assert Verdict.WARN in verdicts


def test_someone_on_notice_is_answered_with_silence():
    t = Throttle(interval=1.0, burst=3)
    for i in range(6):
        t.check("alice", now=0.1 * i)
    assert t.check("alice", now=0.7) is Verdict.SILENT


def test_a_second_burst_escalates():
    t = Throttle(interval=1.0, burst=3)
    for i in range(6):
        t.check("alice", now=0.1 * i)
    # Still inside the safety window, so the flag stands; burst again.
    verdicts = [t.check("alice", now=0.7 + 0.1 * i) for i in range(8)]
    assert Verdict.IGNORE in verdicts


def test_a_quiet_spell_forgives():
    """Someone merely enthusiastic should not be held to it forever."""
    t = Throttle(interval=1.0, safety=5.0, burst=3)
    for i in range(6):
        t.check("alice", now=0.1 * i)
    assert t.check("alice", now=100.0) is Verdict.OK


def test_senders_are_tracked_separately():
    t = Throttle(interval=1.0, burst=3)
    for i in range(6):
        t.check("alice", now=0.1 * i)
    assert t.check("bob", now=0.7) is Verdict.OK
