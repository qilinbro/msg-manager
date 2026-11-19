"""One frozen Feishu message through the real NoneBot Adapter/Bot API.

This is intentionally not a normal NoneBot deployment: there is no init(),
.env, plugin, driver startup, bot-info lookup, WS, webhook, receiver or retry.
The adapter's original _call_api and Bot.call_api perform the message request.
Only lifecycle/configuration and authentication are controlled here. Modern
minimal-scope OAuth is acquired once; the supported fixed-token auth strategy
has no refresh-and-replay flow. A transport gate independently enforces both
fixed requests and their one-attempt budgets before any network access.
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
import json
import re
import ssl
import time
from types import SimpleNamespace
from urllib.parse import parse_qsl

import httpx2
from larkify import Feishu
from nonebot.adapters import Adapter as BaseAdapter
from nonebot.adapters.feishu import Adapter, Bot
from nonebot.adapters.feishu.config import BotConfig

from bridge_common import BridgeError, strict_loads

TOKEN_URL = "https://accounts.feishu.cn/oauth/v3/token"
MESSAGE_URL = "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id"
SEND_SCOPE = "im:message:send_as_bot"
MAX_TEXT_BYTES = 12000
MAX_REQUEST_BYTES = 16 * 1024
MAX_RESPONSE_BYTES = 64 * 1024
MAX_TOKEN_BYTES = 16 * 1024
TIMEOUT_SECONDS = 20

_AUTH_CODES = frozenset({20002, 20009, 20048, 20069, 20130, 20140})
_MESSAGE_AUTH_CODES = frozenset({99991663, 99991665})
_PERMISSION_CODES = frozenset({230006, 99991672, 99991676})
_REJECTED_CODES = frozenset({230001, 230022, 230025, 230028, 230075})
_RECIPIENT_CODES = frozenset({230013, 230029, 230034, 230035, 230053})
_RATE_CODES = frozenset({230020, 99991400, 99991403})


def _header_safe(value: object, maximum: int) -> bool:
    return isinstance(value, str) and 0 < len(value) <= maximum and all(
        0x21 <= ord(char) <= 0x7E for char in value
    )


def _identifier(value: object, prefix: str, maximum: int) -> bool:
    return isinstance(value, str) and len(prefix) < len(value) <= maximum and bool(
        re.fullmatch(re.escape(prefix) + r"[A-Za-z0-9_-]+", value)
    )


def _json_bytes(value: object) -> bytes:
    # Match the Go frozen-content encoding, including HTML/line-separator
    # escaping. The outer HTTP JSON may use a different equivalent encoding,
    # but its parsed content string, text, target and UUID cannot change.
    text = json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
    for source, target in (("<", r"\u003c"), (">", r"\u003e"), ("&", r"\u0026"),
                           ("\u2028", r"\u2028"), ("\u2029", r"\u2029")):
        text = text.replace(source, target)
    return text.encode("utf-8", errors="strict")


@dataclass(frozen=True, repr=False)
class _FrozenInput:
    app_id: str
    app_secret: str
    recipient_open_id: str
    text: str
    uuid: str

    def body(self) -> dict:
        return {"receive_id": self.recipient_open_id, "msg_type": "text",
                "content": _json_bytes({"text": self.text}).decode("utf-8"), "uuid": self.uuid}

    def token_form(self) -> dict:
        return {"grant_type": "client_credentials", "client_id": self.app_id,
                "client_secret": self.app_secret, "scope": SEND_SCOPE}


def _validate_payload(action: str, payload: dict) -> _FrozenInput:
    if action != "feishu.send" or type(payload) is not dict or set(payload) != {
        "app_id", "app_secret", "recipient_open_id", "text", "uuid"
    }:
        raise BridgeError("protocol_error")
    value = _FrozenInput(**payload)
    if not (_identifier(value.app_id, "cli_", 128) and _header_safe(value.app_secret, 4096)
            and _identifier(value.recipient_open_id, "ou_", 128) and _identifier(value.uuid, "", 50)
            and isinstance(value.text, str) and value.text.strip()
            and value.app_secret not in value.text):
        raise BridgeError("protocol_error")
    try:
        if len(value.text.encode("utf-8", errors="strict")) > MAX_TEXT_BYTES:
            raise ValueError
        if any((ord(char) < 0x20 and char not in "\n\t") or ord(char) == 0x7F for char in value.text):
            raise ValueError
        if len(_json_bytes(value.body())) > MAX_REQUEST_BYTES:
            raise ValueError
    except (ValueError, UnicodeError):
        raise BridgeError("protocol_error") from None
    return value


def _make_network_transport() -> httpx2.AsyncBaseTransport:
    tls = ssl.create_default_context()
    tls.minimum_version = ssl.TLSVersion.TLSv1_2
    return httpx2.AsyncHTTPTransport(
        verify=tls, trust_env=False, retries=0, http1=True, http2=False,
        limits=httpx2.Limits(max_connections=1, max_keepalive_connections=0),
    )


class _OperationTransport(httpx2.AsyncBaseTransport):
    """Borrowed by two clients; only shutdown() closes the underlying pool."""

    def __init__(self, frozen: _FrozenInput, inner: httpx2.AsyncBaseTransport):
        self.frozen = frozen
        self.inner = inner
        self.token_attempted = False
        self.message_attempted = False
        self.token: str | None = None
        self.refresh_at = 0.0
        self.message_id: str | None = None
        self.failure: BridgeError | None = None

    def _fail(self, kind: str, *, platform_code: int = 0, not_sent: bool = False):
        failure = BridgeError(kind, platform_code=platform_code,
                              send_state="not_sent" if not_sent else "unknown")
        if self.failure is None:
            self.failure = failure
        raise failure

    def _protocol(self):
        self._fail("send_unknown" if self.message_attempted else "protocol_error",
                   not_sent=not self.message_attempted)

    async def aclose(self):
        # larkify and the token client each close their own borrowed client.
        # Keeping ownership here allows a single operation-scoped attempt gate.
        pass

    async def shutdown(self):
        await self.inner.aclose()

    def _check_request(self, request: httpx2.Request) -> bool:
        if self.failure is not None:
            # A rejected/replayed SDK request terminates the whole operation;
            # an SDK cannot catch the denial and then try a different request.
            raise self.failure
        if request.method != "POST" or "cookie" in request.headers or "proxy-authorization" in request.headers:
            self._protocol()
        if str(request.url) == TOKEN_URL:
            if self.token_attempted or self.message_attempted or "authorization" in request.headers:
                self._protocol()
            if request.headers.get("content-type", "").split(";", 1)[0].lower() != "application/x-www-form-urlencoded":
                self._protocol()
            try:
                pairs = parse_qsl(request.content.decode("ascii"), keep_blank_values=True, strict_parsing=True)
                if len(pairs) != 4 or len(dict(pairs)) != 4 or dict(pairs) != self.frozen.token_form():
                    self._protocol()
            except (UnicodeError, ValueError):
                self._protocol()
            return False
        if str(request.url) != MESSAGE_URL or self.message_attempted or not self.token_attempted or not self.token:
            self._protocol()
        if time.monotonic() >= self.refresh_at:
            self._fail("unavailable", not_sent=True)
        if request.headers.get("authorization") != "Bearer " + self.token:
            self._protocol()
        if request.headers.get("content-type", "").split(";", 1)[0].lower() != "application/json":
            self._protocol()
        try:
            body = strict_loads(request.content, max_bytes=MAX_REQUEST_BYTES)
            if type(body) is not dict or body != self.frozen.body():
                self._protocol()
        except (BridgeError, ValueError, TypeError, UnicodeError):
            self._protocol()
        return True

    async def _bounded_body(self, response: httpx2.Response) -> bytes:
        if response.headers.get("content-type", "").split(";", 1)[0].strip().lower() != "application/json":
            self._protocol()
        if response.headers.get("content-encoding", "identity").strip().lower() not in ("", "identity"):
            self._protocol()
        length = response.headers.get("content-length")
        if length is not None and (not length.isascii() or not length.isdecimal() or int(length) > MAX_RESPONSE_BYTES):
            self._protocol()
        if response.is_stream_consumed:
            body = response.content
            if len(body) > MAX_RESPONSE_BYTES:
                self._protocol()
            return body
        parts, size = [], 0
        async for chunk in response.aiter_raw():
            size += len(chunk)
            if size > MAX_RESPONSE_BYTES:
                self._protocol()
            parts.append(chunk)
        return b"".join(parts)

    def _fields(self, body: bytes) -> dict:
        try:
            fields = strict_loads(body, max_bytes=MAX_RESPONSE_BYTES)
        except (BridgeError, ValueError, TypeError, UnicodeError):
            self._protocol()
        if type(fields) is not dict or type(fields.get("code")) is not int or not -(2**63) <= fields["code"] < 2**63:
            self._protocol()
        return fields

    def _token_response(self, status: int, fields: dict, started: float) -> bytes:
        code = fields["code"]
        if status >= 500:
            self._fail("unavailable", not_sent=True)
        if code:
            if code in _AUTH_CODES:
                self._fail("auth_failed", platform_code=code, not_sent=True)
            if code in (20068, 21001):
                self._fail("permission_denied", platform_code=code, not_sent=True)
            if code in (99991400, 99991403):
                self._fail("rate_limited", platform_code=code, not_sent=True)
            self._fail("unavailable", not_sent=True)
        if not 200 <= status < 300:
            self._fail("unavailable", not_sent=True)
        token, token_type, scope, expiry = (fields.get(key) for key in ("access_token", "token_type", "scope", "expires_in"))
        if not _header_safe(token, MAX_TOKEN_BYTES) or not isinstance(token_type, str) or token_type.lower() != "bearer" or type(expiry) is not int or not 1 <= expiry <= 86400:
            self._fail("unavailable", not_sent=True)
        if not isinstance(scope, str) or " ".join(scope.split()) != SEND_SCOPE:
            self._fail("permission_denied", not_sent=True)
        self.refresh_at = started + expiry - min(expiry / 10, 60)
        if time.monotonic() >= self.refresh_at:
            self._fail("unavailable", not_sent=True)
        if token in self.frozen.text:
            self._fail("protocol_error", not_sent=True)
        self.token = token
        return _json_bytes({"code": 0, "access_token": token, "token_type": "Bearer", "scope": SEND_SCOPE, "expires_in": expiry})

    def _message_response(self, status: int, fields: dict) -> bytes:
        code, data = fields["code"], fields.get("data")
        if status >= 500:
            self._fail("send_unknown")
        if code:
            if data is not None and (type(data) is not dict or data.get("message_id") not in (None, "")):
                self._fail("send_unknown")
            if code in _MESSAGE_AUTH_CODES:
                self._fail("auth_failed", platform_code=code, not_sent=True)
            if code in _PERMISSION_CODES:
                self._fail("permission_denied", platform_code=code, not_sent=True)
            if code in _RATE_CODES:
                self._fail("rate_limited", platform_code=code, not_sent=True)
            if code in _REJECTED_CODES or code in _RECIPIENT_CODES:
                self._fail("platform_rejected", platform_code=code, not_sent=True)
            self._fail("send_unknown", platform_code=230049 if code == 230049 else 0)
        if not 200 <= status < 300 or type(data) is not dict:
            self._fail("send_unknown")
        message_id = data.get("message_id")
        if not _header_safe(message_id, 256) or any(
            value in message_id for value in (self.frozen.app_secret, self.token, self.frozen.recipient_open_id)
        ):
            self._fail("send_unknown")
        self.message_id = message_id
        # Only a minimal validated receipt reaches larkify's generic response
        # parser. In particular no raw platform msg/log_id enters exceptions.
        return _json_bytes({"code": 0, "data": {"message_id": message_id}})

    async def handle_async_request(self, request: httpx2.Request) -> httpx2.Response:
        is_message = self._check_request(request)
        if is_message:
            self.message_attempted = True
        else:
            self.token_attempted = True
        started = time.monotonic()
        response = None
        try:
            response = await self.inner.handle_async_request(request)
            body = await self._bounded_body(response)
            fields = self._fields(body)
            safe_body = (self._message_response(response.status_code, fields) if is_message
                         else self._token_response(response.status_code, fields, started))
            return httpx2.Response(response.status_code, headers={"Content-Type": "application/json"},
                                   content=safe_body, request=request)
        except BridgeError:
            raise
        except asyncio.CancelledError:
            self._fail("canceled", not_sent=not self.message_attempted)
        except Exception:
            self._fail("send_unknown" if self.message_attempted else "unavailable",
                       not_sent=not self.message_attempted)
        finally:
            if response is not None:
                try:
                    await response.aclose()
                except (Exception, asyncio.CancelledError):
                    pass


class _SendOnlyAdapter(Adapter):
    def __init__(self, client: Feishu):
        # Calling Adapter.__init__ would read NoneBot's global configuration and
        # register startup/receiver hooks. The real API implementation needs
        # only BaseAdapter state and the supported larkify client injection.
        BaseAdapter.__init__(self, SimpleNamespace(config=SimpleNamespace(api_timeout=TIMEOUT_SECONDS)))
        self._send_client = client

    def create_client(self, bot_config: BotConfig) -> Feishu:
        return self._send_client


async def handle(action: str, payload: dict) -> dict:
    frozen = _validate_payload(action, payload)
    operation = None
    try:
        operation = _OperationTransport(frozen, _make_network_transport())
        async with httpx2.AsyncClient(transport=operation, follow_redirects=False, trust_env=False,
                                     timeout=TIMEOUT_SECONDS, auth=None) as client:
            await client.post(TOKEN_URL, data=frozen.token_form(),
                              headers={"Accept": "application/json", "Accept-Encoding": "identity"})
        if not operation.token:
            operation._fail("unavailable", not_sent=True)
        # Passing a string is larkify's supported TokenAuthStrategy, not
        # AppAuthStrategy: no refresh request or auth-flow replay exists here.
        client = Feishu(operation.token, base_url="https://open.feishu.cn",
                        oauth_base_url="https://accounts.feishu.cn", timeout=TIMEOUT_SECONDS,
                        async_transport=operation, follow_redirects=False, trust_env=False,
                        proxy=None, auto_retry=False, insecure_allow_http=False)
        adapter = _SendOnlyAdapter(client)
        bot_config = BotConfig(app_id=frozen.app_id, app_secret=frozen.app_secret,
                               verification_token="unused-send-only", protocol="http")
        # The create-message API does not consume bot-info metadata. None means
        # it was NOT fetched/verified; no discovery request is silently added.
        bot = Bot(adapter, frozen.app_id, bot_config=bot_config, bot_info=None)
        bot._calling_api_hook = frozenset()
        bot._called_api_hook = frozenset()
        result = await bot.call_api("im/v1/messages", method="POST",
                                    params={"receive_id_type": "open_id"}, json=frozen.body(),
                                    headers={"Accept": "application/json", "Accept-Encoding": "identity"})
        if type(result) is not dict or result != {"code": 0, "data": {"message_id": operation.message_id}} or not operation.message_id:
            operation._fail("send_unknown")
        return {"message_id": operation.message_id}
    except BridgeError:
        raise
    except asyncio.CancelledError:
        if operation is not None:
            operation._fail("canceled", not_sent=not operation.message_attempted)
        raise BridgeError("canceled") from None
    except Exception:
        if operation is not None and operation.failure is not None:
            raise operation.failure from None
        raise BridgeError("send_unknown" if operation is not None and operation.message_attempted else "unavailable",
                          send_state="unknown" if operation is not None and operation.message_attempted else "not_sent") from None
    finally:
        if operation is not None:
            try:
                await operation.shutdown()
            except (Exception, asyncio.CancelledError):
                pass
