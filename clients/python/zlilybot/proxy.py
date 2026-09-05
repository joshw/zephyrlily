"""Getting a zlily proxy to talk to: spawn one, or attach to a running one."""

from __future__ import annotations

import asyncio
import contextlib
import logging
import os
import shutil
import socket
import subprocess
from dataclasses import dataclass, field
from pathlib import Path

import httpx

from .client import normalize_base_url
from .errors import ConfigError, ProxyStartError

log = logging.getLogger(__name__)

DEFAULT_LILY = "rpi.lily.org:7777"
READY_TIMEOUT = 20.0
_BIND_RETRIES = 3


def resolve_zlily(explicit: str | os.PathLike[str] | None) -> str:
    """Find the zlily binary.

    An explicitly configured path that does not resolve is fatal rather than a
    silent fallback to PATH: falling back would quietly run a different build
    than the operator asked for, which is exactly the confusion the setting
    exists to prevent.
    """
    if explicit:
        p = Path(explicit).expanduser()
        if not p.exists():
            raise ConfigError(f"zlily_path points at {p}, which does not exist")
        if p.is_dir() or not os.access(p, os.X_OK):
            raise ConfigError(f"zlily_path points at {p}, which is not executable")
        return str(p)

    found = shutil.which("zlily")
    if not found:
        raise ConfigError(
            "zlily was not found on PATH; install it, or set proxy.zlily_path "
            "(or pass --zlily) to name the binary explicitly"
        )
    return found


def _free_port() -> int:
    """Ask the OS for an unused loopback port.

    There is a small race between closing this socket and the child binding it;
    the caller retries on a bind failure.
    """
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@dataclass
class ProxyHandle:
    """Where the proxy is, and the child process if we started it."""

    base_url: str
    process: asyncio.subprocess.Process | None = None
    command: list[str] = field(default_factory=list)

    @property
    def spawned(self) -> bool:
        return self.process is not None

    @property
    def running(self) -> bool:
        return self.process is not None and self.process.returncode is None


class ProxyRunner:
    """Supplies a proxy base URL, spawning `zlily server` when asked to.

    In attach mode nothing is started or stopped — the URL is simply handed
    back, so an externally managed proxy is never disturbed.
    """

    def __init__(
        self,
        *,
        mode: str,
        url: str | None = None,
        lily: str = DEFAULT_LILY,
        tls: bool = False,
        zlily_path: str | None = None,
        log_level: str = "warn",
    ) -> None:
        if mode not in ("spawn", "attach"):
            raise ConfigError(f"proxy mode must be 'spawn' or 'attach', not {mode!r}")
        self.mode = mode
        self.lily = lily or DEFAULT_LILY
        self.tls = tls
        self.zlily_path = zlily_path
        self.log_level = log_level
        self.handle: ProxyHandle | None = None
        self._stderr_tail: list[str] = []
        self._drain_task: asyncio.Task | None = None

        if mode == "attach":
            if not url:
                raise ConfigError("proxy mode is 'attach' but no proxy url was given")
            self._url = normalize_base_url(url)
        else:
            self._url = ""
            # Resolve eagerly so a bad path fails at startup, not at first use.
            self._binary = resolve_zlily(zlily_path)

    async def start(self) -> ProxyHandle:
        if self.mode == "attach":
            self.handle = ProxyHandle(base_url=self._url)
            log.info("using the proxy at %s", self._url)
            return self.handle

        last: Exception | None = None
        for attempt in range(_BIND_RETRIES):
            port = _free_port()
            try:
                self.handle = await self._spawn(port)
                return self.handle
            except ProxyStartError as e:
                last = e
                await self.stop()
                log.warning("proxy failed to start on port %d (%s); retrying", port, e)
                if attempt == _BIND_RETRIES - 1:
                    break
        raise ProxyStartError(f"could not start zlily server: {last}")

    async def _spawn(self, port: int) -> ProxyHandle:
        listen = f"127.0.0.1:{port}"
        cmd = [
            self._binary, "server",
            "--listen", listen,
            "--lily", self.lily,
            "--log-level", self.log_level,
        ]
        if self.tls:
            cmd.append("--tls")

        log.info("starting %s", " ".join(cmd))
        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            # Its own process group, so a Ctrl-C in our terminal does not race
            # us to the child; we shut it down deliberately in stop().
            start_new_session=True,
        )
        self._stderr_tail = []
        self._drain_task = asyncio.create_task(self._drain_stderr(proc))

        base_url = f"http://{listen}"
        await self._await_ready(proc, base_url)
        return ProxyHandle(base_url=base_url, process=proc, command=cmd)

    async def _await_ready(self, proc: asyncio.subprocess.Process, base_url: str) -> None:
        """Poll GET /info until the proxy answers, or the child dies."""
        deadline = asyncio.get_running_loop().time() + READY_TIMEOUT
        async with httpx.AsyncClient(base_url=base_url, timeout=2.0) as http:
            while True:
                if proc.returncode is not None:
                    raise ProxyStartError(
                        f"zlily server exited with status {proc.returncode}"
                        f"{self._stderr_note()}"
                    )
                try:
                    r = await http.get("/info")
                    if r.status_code == 200:
                        info = r.json()
                        log.info(
                            "proxy ready at %s (zlily %s, lily %s)",
                            base_url,
                            info.get("version", "version unknown"),
                            info.get("lily_addr", "?"),
                        )
                        return
                except httpx.HTTPError:
                    pass
                if asyncio.get_running_loop().time() >= deadline:
                    raise ProxyStartError(
                        f"zlily server did not become ready within "
                        f"{READY_TIMEOUT:.0f}s{self._stderr_note()}"
                    )
                await asyncio.sleep(0.1)

    async def _drain_stderr(self, proc: asyncio.subprocess.Process) -> None:
        """Keep the child's stderr flowing and remember the tail for errors.

        Without a reader the pipe fills and the proxy blocks on its own logging.
        """
        assert proc.stderr is not None
        try:
            async for raw in proc.stderr:
                line = raw.decode("utf-8", "replace").rstrip()
                if not line:
                    continue
                self._stderr_tail.append(line)
                del self._stderr_tail[:-20]
                log.debug("zlily: %s", line)
        except asyncio.CancelledError:
            raise
        except Exception:  # noqa: BLE001 - the child is going away regardless
            pass

    def _stderr_note(self) -> str:
        if not self._stderr_tail:
            return ""
        return "; last output: " + " | ".join(self._stderr_tail[-5:])

    async def ensure_running(self) -> ProxyHandle:
        """Return a live proxy, starting or restarting one if need be.

        Safe to call before the first start(), which is how the session brings
        the proxy up on its own.
        """
        if self.mode == "attach":
            return self.handle if self.handle is not None else await self.start()
        if self.handle is not None and self.handle.running:
            return self.handle
        if self.handle is not None:
            log.warning("zlily server exited (status %s); restarting",
                        self.handle.process.returncode if self.handle.process else "?")
        await self.stop()
        return await self.start()

    async def stop(self) -> None:
        """Terminate a spawned child. Attached proxies are left alone."""
        if self._drain_task is not None:
            self._drain_task.cancel()
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await self._drain_task
            self._drain_task = None

        handle, self.handle = self.handle, None
        if handle is None or handle.process is None:
            return
        proc = handle.process
        if proc.returncode is not None:
            return
        proc.terminate()
        try:
            await asyncio.wait_for(proc.wait(), timeout=5.0)
        except (asyncio.TimeoutError, TimeoutError):
            log.warning("zlily server did not exit; killing it")
            proc.kill()
            with contextlib.suppress(Exception):
                await proc.wait()

    async def __aenter__(self) -> ProxyHandle:
        return await self.start()

    async def __aexit__(self, *exc) -> None:
        await self.stop()
