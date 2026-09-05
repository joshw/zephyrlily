"""The spawn path: starting, waiting for, and reaping a `zlily server`.

A stub binary stands in for the real one so these run without a Go build.
"""

from __future__ import annotations

import asyncio
import os
import stat
import sys
from pathlib import Path

import pytest

from zlilybot.errors import ProxyStartError
from zlilybot.proxy import ProxyRunner

PROJECT_DIR = Path(__file__).parent.parent

STUB = """#!{python}
import asyncio, sys
sys.path.insert(0, {root!r})
from zlilybot.testing import FakeProxy

def listen_addr(argv):
    return argv[argv.index("--listen") + 1]

async def main():
    if sys.argv[1:2] == ["version"]:
        print("zlily v0.0.0-stub")
        return
    host, _, port = listen_addr(sys.argv).partition(":")
    proxy = FakeProxy()
    await proxy.start(host, int(port))
    print("stub proxy up", file=sys.stderr, flush=True)
    await asyncio.Event().wait()

asyncio.run(main())
"""

FAILING_STUB = """#!{python}
import sys
print("cannot bind: address in use", file=sys.stderr)
sys.exit(2)
"""


def _install(tmp_path: Path, body: str) -> Path:
    binary = tmp_path / "zlily"
    binary.write_text(body.format(python=sys.executable, root=str(PROJECT_DIR)))
    binary.chmod(binary.stat().st_mode | stat.S_IEXEC | stat.S_IXGRP)
    return binary


async def test_spawns_and_becomes_ready(tmp_path):
    binary = _install(tmp_path, STUB)
    runner = ProxyRunner(mode="spawn", zlily_path=str(binary), lily="fake:7777")
    handle = await runner.start()
    try:
        assert handle.spawned and handle.running
        assert handle.base_url.startswith("http://127.0.0.1:")
        # The port it was told to listen on is the one we polled for /info.
        assert "--listen" in handle.command
        listen = handle.command[handle.command.index("--listen") + 1]
        assert handle.base_url.endswith(listen)
        assert "--lily" in handle.command and "fake:7777" in handle.command
    finally:
        await runner.stop()


async def test_child_is_reaped_on_stop(tmp_path):
    binary = _install(tmp_path, STUB)
    runner = ProxyRunner(mode="spawn", zlily_path=str(binary))
    handle = await runner.start()
    pid = handle.process.pid
    await runner.stop()
    assert handle.process.returncode is not None

    # Signal 0 probes for existence without delivering anything.
    for _ in range(50):
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return
        await asyncio.sleep(0.02)
    raise AssertionError("child survived stop()")


async def test_a_child_that_dies_is_reported_with_its_output(tmp_path):
    binary = _install(tmp_path, FAILING_STUB)
    runner = ProxyRunner(mode="spawn", zlily_path=str(binary))
    with pytest.raises(ProxyStartError) as excinfo:
        await runner.start()
    await runner.stop()
    assert "address in use" in str(excinfo.value)


async def test_ensure_running_restarts_a_dead_child(tmp_path):
    binary = _install(tmp_path, STUB)
    runner = ProxyRunner(mode="spawn", zlily_path=str(binary))
    first = await runner.start()
    try:
        first.process.kill()
        await first.process.wait()
        second = await runner.ensure_running()
        assert second.running
        assert second.process.pid != first.process.pid
    finally:
        await runner.stop()


async def test_attach_mode_starts_nothing():
    runner = ProxyRunner(mode="attach", url="127.0.0.1:9999")
    handle = await runner.start()
    assert not handle.spawned
    assert handle.base_url == "http://127.0.0.1:9999"
    await runner.stop()  # must not raise
