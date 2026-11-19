"""Small, dependency-free IPC contract shared by the two real adapters."""
import json
import math

KINDS = frozenset({
    "not_configured", "invalid_config", "unreachable", "auth_failed",
    "rate_limited", "protocol_error", "identity_mismatch", "webhook_present",
    "canceled", "permission_denied", "platform_rejected", "send_unknown",
    "unavailable",
})
SEND_STATES = frozenset({"not_sent", "attempted", "responded", "unknown"})


class BridgeError(Exception):
    """Only fixed diagnostics cross the process boundary; never exception repr."""

    def __init__(self, kind: str, *, retry_after_seconds: int = 0,
                 platform_code: int = 0, send_state: str = "not_sent"):
        self.kind = kind if kind in KINDS else "unavailable"
        self.retry_after_seconds = retry_after_seconds if (
            type(retry_after_seconds) is int and 0 <= retry_after_seconds <= 86400
        ) else 0
        self.platform_code = platform_code if (
            type(platform_code) is int and 0 <= platform_code <= 2147483647
        ) else 0
        self.send_state = send_state if send_state in SEND_STATES else "unknown"
        super().__init__("NoneBot bridge: " + self.kind)

    def as_dict(self) -> dict:
        return {"kind": self.kind, "retry_after_seconds": self.retry_after_seconds,
                "platform_code": self.platform_code, "send_state": self.send_state}


def _object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise BridgeError("protocol_error")
        result[key] = value
    return result


def _constant(_):
    raise BridgeError("protocol_error")


def _check(value, depth=0):
    if depth > 32:
        raise BridgeError("protocol_error")
    if isinstance(value, dict):
        for key, item in value.items():
            _check(key, depth + 1)
            _check(item, depth + 1)
    elif isinstance(value, list):
        for item in value:
            _check(item, depth + 1)
    elif isinstance(value, str):
        try:
            value.encode("utf-8", "strict")
        except UnicodeError:
            raise BridgeError("protocol_error") from None
    elif isinstance(value, float) and not math.isfinite(value):
        raise BridgeError("protocol_error")


def strict_loads(data: str | bytes, max_bytes: int = 4194304):
    try:
        if isinstance(data, bytes):
            if len(data) > max_bytes:
                raise BridgeError("protocol_error")
            text = data.decode("utf-8", "strict")
        elif isinstance(data, str):
            if len(data.encode("utf-8", "strict")) > max_bytes:
                raise BridgeError("protocol_error")
            text = data
        else:
            raise BridgeError("protocol_error")
        result = json.loads(text, object_pairs_hook=_object, parse_constant=_constant)
        _check(result)
        return result
    except BridgeError:
        raise
    except (ValueError, UnicodeError, RecursionError, OverflowError):
        raise BridgeError("protocol_error") from None
