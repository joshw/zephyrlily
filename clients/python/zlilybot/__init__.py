"""zlilybot — build Lily bots against a zlily proxy."""

from .bot import Bot
from .client import State, ZlilyClient
from .entities import Entities, same_name, to_lily_name
from .errors import (
    AuthError,
    ConfigError,
    Disconnected,
    NameUnavailable,
    ProxyStartError,
    TransientAuthError,
    ZlilyError,
)
from .events import Entity, Event, Message, ServerMessage, unwrap_emote
from .proxy import ProxyHandle, ProxyRunner, resolve_zlily
from .session import Session
from .throttle import Throttle, Verdict

__all__ = [
    "Bot", "Session", "ZlilyClient", "State",
    "ProxyRunner", "ProxyHandle", "resolve_zlily",
    "Entities", "Entity", "Event", "Message", "ServerMessage",
    "to_lily_name", "same_name", "unwrap_emote",
    "Throttle", "Verdict",
    "ZlilyError", "ConfigError", "ProxyStartError",
    "AuthError", "TransientAuthError", "NameUnavailable", "Disconnected",
]
