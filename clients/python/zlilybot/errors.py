"""Exceptions raised by zlilybot."""


class ZlilyError(Exception):
    """Base class for every error this library raises."""


class ConfigError(ZlilyError):
    """The configuration is unusable — a missing binary, a malformed URL."""


class ProxyStartError(ZlilyError):
    """A spawned `zlily server` could not be started or never became ready."""


class AuthError(ZlilyError):
    """The proxy refused our credentials.

    Fatal by design: retrying a rejected password just burns through the
    proxy's per-IP failure budget and earns a lockout.
    """


class TransientAuthError(ZlilyError):
    """The proxy could not authenticate us *right now* — rate limited or at
    capacity. Worth retrying after `retry_after` seconds."""

    def __init__(self, message: str, retry_after: float | None = None):
        super().__init__(message)
        self.retry_after = retry_after


class NameUnavailable(ZlilyError):
    """A configured bot name could not be claimed via /rename."""


class Disconnected(ZlilyError):
    """The link to the proxy or to Lily went away; the supervisor should
    re-establish the session."""
