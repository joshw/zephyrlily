"""Interactive config generation.

Writes a starter TOML after checking that the settings actually work, so an
operator finds out about a wrong password here rather than from a crash loop.
"""

from __future__ import annotations

import asyncio
import getpass
import os
import sys
from pathlib import Path

from .client import ZlilyClient, normalize_base_url
from .errors import ConfigError
from .proxy import DEFAULT_LILY, ProxyRunner, resolve_zlily

DEFAULT_URL = "http://127.0.0.1:7888"


def ask(prompt: str, default: str = "") -> str:
    suffix = f" [{default}]" if default else ""
    try:
        answer = input(f"{prompt}{suffix}: ").strip()
    except EOFError:
        answer = ""
    return answer or default


def ask_bool(prompt: str, default: bool = False) -> bool:
    d = "Y/n" if default else "y/N"
    while True:
        raw = ask(f"{prompt} ({d})").strip().lower()
        if not raw:
            return default
        if raw in ("y", "yes"):
            return True
        if raw in ("n", "no"):
            return False
        print("  please answer y or n")


def ask_choice(prompt: str, choices: dict[str, str], default: str) -> str:
    print(f"\n{prompt}")
    for key, desc in choices.items():
        print(f"  {key:<8} {desc}")
    while True:
        raw = ask("choice", default).strip().lower()
        if raw in choices:
            return raw
        print(f"  pick one of: {', '.join(choices)}")


class ProxyAnswers:
    def __init__(self) -> None:
        self.mode = "spawn"
        self.url = DEFAULT_URL
        self.lily = DEFAULT_LILY
        self.tls = False
        self.zlily_path: str | None = None

    def runner(self) -> ProxyRunner:
        return ProxyRunner(
            mode=self.mode,
            url=self.url,
            lily=self.lily,
            tls=self.tls,
            zlily_path=self.zlily_path,
        )


def prompt_proxy() -> ProxyAnswers:
    a = ProxyAnswers()
    a.mode = ask_choice(
        "How should the bot reach Lily?",
        {
            "spawn": "run its own `zlily server` (needs the zlily binary)",
            "attach": "connect to a proxy that is already running",
        },
        "spawn",
    )

    if a.mode == "attach":
        while True:
            raw = ask("Proxy URL", DEFAULT_URL)
            try:
                a.url = normalize_base_url(raw)
                return a
            except ConfigError as e:
                print(f"  {e}")

    # spawn
    while True:
        given = ask("Path to the zlily binary (blank to search PATH)").strip()
        try:
            resolved = resolve_zlily(given or None)
        except ConfigError as e:
            print(f"  {e}")
            continue
        # Record the path only when the operator named one; otherwise leave it
        # unset so the config keeps following PATH.
        a.zlily_path = resolved if given else None
        print(f"  using {resolved} ({_zlily_version(resolved)})")
        break

    a.lily = ask("Lily server address", DEFAULT_LILY)
    a.tls = ask_bool("Connect to Lily over TLS?", False)
    return a


def _zlily_version(binary: str) -> str:
    import subprocess

    try:
        out = subprocess.run(
            [binary, "version"], capture_output=True, text=True, timeout=10
        )
        return (out.stdout or out.stderr).strip().splitlines()[0]
    except Exception:  # noqa: BLE001 - a version banner is a nicety
        return "version unknown"


async def verify(proxy: ProxyAnswers, username: str, password: str) -> str:
    """Start or contact the proxy, log in, and report what came back.

    Deliberately stops at /session rather than /state: /state blocks until the
    SLCP sync finishes, which a prompt pending at login gates, and clearing
    that needs a WebSocket reader this check has no business running. /auth
    already proves the credentials -- it is the call that returns 401.
    """
    runner = proxy.runner()
    try:
        handle = await runner.start()
        client = ZlilyClient(handle.base_url)
        try:
            info = await client.info()
            await client.auth(username, password)
            session = await client.session()
            return (
                f"logged in as {session.get('username', username)} on "
                f"{info.get('lily_addr', '?')}"
            )
        finally:
            await client.aclose()
    finally:
        await runner.stop()


def render(
    proxy: ProxyAnswers,
    *,
    username: str,
    password: str | None,
    password_env: str | None,
    bot_name: str | None,
    owners: list[str],
    join: list[str],
    store_path: str,
    extra: str = "",
) -> str:
    lines = ["[proxy]", f'mode = "{proxy.mode}"', ""]
    if proxy.mode == "spawn":
        lines += [
            f'lily = "{proxy.lily}"',
            f"tls = {str(proxy.tls).lower()}",
        ]
        if proxy.zlily_path:
            lines.append(f'zlily_path = "{proxy.zlily_path}"')
        else:
            lines.append("# zlily_path = \"/usr/local/bin/zlily\"  # unset: search PATH")
    else:
        lines.append(f'url = "{proxy.url}"')

    lines += ["", "[lily]", f'username = "{username}"']
    if password_env:
        lines.append(f'password_env = "{password_env}"')
    else:
        lines.append(f'password = "{_toml_str(password or "")}"')
    lines.append(f"join = [{', '.join(_toml_quote(d) for d in join)}]")

    lines += ["", "[bot]"]
    if bot_name:
        lines.append(f'name = "{bot_name}"')
    else:
        lines.append('# name = "MechaJosh"   # unset: keep the account\'s current name')
    lines.append(f"owners = [{', '.join(_toml_quote(o) for o in owners)}]")

    if extra:
        lines += ["", extra.strip()]

    lines += ["", "[store]", f'path = "{_toml_str(store_path)}"', ""]
    return "\n".join(lines)


def _toml_str(s: str) -> str:
    return s.replace("\\", "\\\\").replace('"', '\\"')


def _toml_quote(s: str) -> str:
    return f'"{_toml_str(s)}"'


def run_init(
    path: Path,
    *,
    force: bool = False,
    default_env: str = "LILY_PASSWORD",
    default_store: str = "bot.db",
    extra: str = "",
) -> int:
    """Walk the operator through a config and write it. Returns an exit status."""
    if path.exists() and not force:
        print(f"{path} already exists; pass --force to overwrite it.", file=sys.stderr)
        return 1

    print(f"Writing a new configuration to {path}.")
    print("Press enter to accept the value in brackets.\n")

    proxy = prompt_proxy()

    print()
    username = ask("Lily username")
    while not username:
        username = ask("Lily username (required)")
    password = getpass.getpass("Lily password (not echoed): ")

    use_env = ask_bool(
        f"\nKeep the password out of the file and read it from ${default_env}?", True
    )
    password_env = default_env if use_env else None

    bot_name = ask(
        "\nName to claim on Lily (blank to keep the account's current name)"
    ).strip() or None

    owners_raw = ask(
        "Owners, comma separated -- they may send $ admin commands privately", ""
    ).strip()
    owners = [o.strip() for o in owners_raw.split(",") if o.strip()]

    join_raw = ask("Discussions to join, comma separated", "").strip()
    join = [d.strip() for d in join_raw.split(",") if d.strip()]
    store_path = ask("Database path", default_store)

    print("\nChecking the settings...")
    try:
        result = asyncio.run(verify(proxy, username, password))
        print(f"  ok — {result}")
    except KeyboardInterrupt:
        raise
    except Exception as e:  # noqa: BLE001
        # Anything here is a failed check, not a crash: an httpx timeout, a
        # refused connection, a proxy that would not start. Report it in one
        # line and let the operator decide, rather than dumping a traceback on
        # someone halfway through a setup wizard.
        print(f"  failed: {type(e).__name__}: {e}")
        if not ask_bool("Write the configuration anyway?", False):
            print("Nothing written.")
            return 1

    text = render(
        proxy,
        username=username,
        password=password,
        password_env=password_env,
        bot_name=bot_name,
        owners=owners,
        join=join,
        store_path=store_path,
        extra=extra,
    )
    path.write_text(text, encoding="utf-8")
    if password_env is None:
        # The file holds a live credential; keep it to the owner.
        os.chmod(path, 0o600)

    print(f"\nWrote {path}.")
    if password_env:
        print(f"Set the password before starting: export {password_env}=...")
    return 0
