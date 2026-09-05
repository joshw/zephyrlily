"""Unit tests for zlilybot's non-networked pieces."""

from __future__ import annotations

import os
import stat
from pathlib import Path

import pytest

from zlilybot.client import normalize_base_url
from zlilybot.entities import Entities, same_name, to_lily_name
from zlilybot.errors import ConfigError
from zlilybot.events import Entity, Event, ServerMessage, unwrap_emote
from zlilybot.proxy import resolve_zlily


# ── URL handling ─────────────────────────────────────────────────────────────


@pytest.mark.parametrize(
    "given,expected",
    [
        ("127.0.0.1:7888", "http://127.0.0.1:7888"),
        ("http://host:1/", "http://host:1"),
        ("https://lily.example.org", "https://lily.example.org"),
        ("ws://host:2", "http://host:2"),
        ("wss://host:2", "https://host:2"),
    ],
)
def test_normalize_base_url(given, expected):
    assert normalize_base_url(given) == expected


def test_url_with_a_path_is_refused():
    """internal/tui/client/addr.go rejects these, so we should fail early."""
    with pytest.raises(ConfigError, match="host root"):
        normalize_base_url("http://host:7888/proxy")


def test_empty_url_is_refused():
    with pytest.raises(ConfigError):
        normalize_base_url("   ")


# ── locating zlily ───────────────────────────────────────────────────────────


def test_explicit_path_is_used(tmp_path):
    binary = tmp_path / "zlily"
    binary.write_text("#!/bin/sh\n")
    binary.chmod(binary.stat().st_mode | stat.S_IEXEC)
    assert resolve_zlily(str(binary)) == str(binary)


def test_missing_explicit_path_is_fatal(tmp_path):
    """It must not quietly fall back to PATH and run a different build."""
    with pytest.raises(ConfigError, match="does not exist"):
        resolve_zlily(str(tmp_path / "nope"))


def test_non_executable_explicit_path_is_fatal(tmp_path):
    binary = tmp_path / "zlily"
    binary.write_text("not executable")
    binary.chmod(0o644)
    with pytest.raises(ConfigError, match="not executable"):
        resolve_zlily(str(binary))


def test_unset_path_searches_path(tmp_path, monkeypatch):
    binary = tmp_path / "zlily"
    binary.write_text("#!/bin/sh\n")
    binary.chmod(binary.stat().st_mode | stat.S_IEXEC)
    monkeypatch.setenv("PATH", str(tmp_path))
    assert resolve_zlily(None) == str(binary)


def test_missing_from_path_names_the_setting(tmp_path, monkeypatch):
    monkeypatch.setenv("PATH", str(tmp_path))
    with pytest.raises(ConfigError, match="zlily_path"):
        resolve_zlily(None)


# ── names and destinations ───────────────────────────────────────────────────


def test_destination_formatting():
    e = Entities()
    e.replace_all([
        Entity(handle="#1", kind="user", name="Alice Smith"),
        Entity(handle="#10", kind="disc", name="the lounge"),
    ])
    assert e.dest("#1") == "Alice_Smith"
    assert e.dest("#10") == "-the_lounge"
    assert e.dest_list(["#1", "#10"]) == "Alice_Smith,-the_lounge"
    assert e.nick("#1") == "Alice_Smith"


def test_same_name_ignores_spacing_and_case():
    assert same_name("Mecha Josh", "mecha_josh")
    assert not same_name("MechaJosh", "mecha_josh")


def test_emote_discussion_detection():
    e = Entities()
    e.replace_all([
        Entity(handle="#10", kind="disc", name="test"),
        Entity(handle="#11", kind="disc", name="emotes", attrib="emote"),
    ])
    assert e.is_emote_dest(["#11"])
    assert not e.is_emote_dest(["#10"])


def test_event_snapshot_does_not_blank_known_fields():
    """A per-event entity map carries only what the proxy filled in."""
    e = Entities()
    e.replace_all([Entity(handle="#1", kind="user", name="Alice", blurb="busy")])
    e.apply(Event(event="public", source="#1",
                  entities={"#1": Entity(handle="#1", kind="user", name="Alice")}))
    assert e.get("#1").blurb == "busy"


def test_rename_event_updates_the_name():
    e = Entities()
    e.replace_all([Entity(handle="#1", kind="user", name="Alice")])
    e.apply(Event(event="rename", source="#1", value="Alicia"))
    assert e.name("#1") == "Alicia"


# ── emotes ───────────────────────────────────────────────────────────────────


@pytest.mark.parametrize(
    "value,expected",
    [
        (" waves hello", "Alice waves hello"),
        (" . o O (is it lunchtime)", "is it lunchtime"),
        (' asks, "where is the coffee"', "where is the coffee"),
        (' says, "hello there"', "hello there"),
    ],
)
def test_unwrap_emote(value, expected):
    assert unwrap_emote("Alice", value) == expected


# ── wire envelope ────────────────────────────────────────────────────────────


def test_prompt_and_error_carry_bare_strings():
    """Unlike every other type, whose data is an object."""
    assert ServerMessage.from_json({"id": 1, "type": "prompt", "data": "go?"}).as_text == "go?"
    assert ServerMessage.from_json({"id": 2, "type": "error", "data": "boom"}).as_text == "boom"
    msg = ServerMessage.from_json({"id": 3, "type": "text", "data": {"text": "hi"}})
    assert msg.as_text == "hi"


def test_event_parsing():
    msg = ServerMessage.from_json({
        "id": 4, "type": "event",
        "data": {"event": "public", "source": "#2", "recips": ["#10"],
                 "value": "hello", "notify": True,
                 "entities": {"#2": {"kind": "user", "name": "Alice"}}},
    })
    ev = msg.event
    assert ev.event == "public" and ev.recips == ("#10",)
    assert ev.entities["#2"].name == "Alice"


# ── encoding ─────────────────────────────────────────────────────────────────


def test_unicode_is_left_for_the_proxy_to_fold():
    """SLCP is 7-bit, but `Conn.Send` in the proxy folds every outbound line,
    so a client hands text over as typed. See internal/lily/conn.go."""
    from zlilybot.session import Session

    import inspect

    source = inspect.getsource(Session.send)
    assert "ascify" not in source


# ── the version handshake ────────────────────────────────────────────────────


async def test_info_reports_the_proxy_build():
    """A client released separately from the proxy has no other way to tell
    what it is talking to, and /info is the only thing it can ask before
    authenticating."""
    from zlilybot import ZlilyClient
    from zlilybot.testing import FakeProxy

    async with FakeProxy(version="1.2.3") as proxy:
        client = ZlilyClient(proxy.base_url)
        try:
            info = await client.info()
            assert info["version"] == "1.2.3"
            assert info["lily_addr"] == "fake.lily.org:7777"
        finally:
            await client.aclose()
