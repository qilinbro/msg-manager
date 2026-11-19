"""A single receive-only operation through the real NoneBot Telegram adapter.

The upstream Adapter builds the API requests and Bot validates the typed API
results. Only its lifecycle is disabled. A restricted NoneBot HTTP driver owns
bounded transport and strips non-authorized payloads *before* model validation;
no event handler, plugin, webhook, polling task, or sending API is started.
"""
import asyncio
import json
import re
import time

import httpx
from loguru import logger
from nonebot.config import Config as NoneBotConfig, Env
from nonebot.drivers import Response
from nonebot.drivers.httpx import Driver as HTTPXDriver
from nonebot.adapters.telegram import Adapter, Bot
from nonebot.adapters.telegram.config import BotConfig

from bridge_common import BridgeError, strict_loads

logger.remove()

_MAX_ID = (1 << 52) - 1
_MAX_UPDATE_ID = (1 << 63) - 2
_MAX_OFFSET = (1 << 63) - 1
_MAX_RESPONSE = 4 << 20
_MAX_BODY = 16000
_TOKEN = re.compile(r"([1-9][0-9]{0,15}):[A-Za-z0-9_-]{20,128}\Z", re.ASCII)
_TYPES = frozenset({
    "message", "edited_message", "channel_post", "edited_channel_post",
    "business_connection", "business_message", "edited_business_message",
    "deleted_business_messages", "message_reaction", "message_reaction_count",
    "inline_query", "chosen_inline_result", "callback_query", "shipping_query",
    "pre_checkout_query", "purchased_paid_media", "poll", "poll_answer",
    "my_chat_member", "chat_member", "chat_join_request", "chat_boost",
    "removed_chat_boost",
})
_EDITS = frozenset({"edited_message", "edited_channel_post", "edited_business_message"})
_MEDIA = frozenset({
    "animation", "audio", "document", "paid_media", "photo", "sticker", "story",
    "video", "video_note", "voice", "contact", "dice", "game", "poll", "venue",
    "location", "caption", "caption_entities", "media_group_id",
})
# Text metadata is ignored, never copied into the inbox or unsupported records.
# Unknown new content fields fail closed instead of silently becoming plain text.
_TEXT_FIELDS = frozenset({
    "message_id", "from", "date", "chat", "text", "entities",
    "link_preview_options", "has_protected_content", "reply_to_message",
    "external_reply", "quote", "reply_to_story", "forward_origin", "via_bot",
    "reply_markup", "effect_id", "author_signature",
})


def _decimal(value, maximum, *, positive=False):
    return (isinstance(value, str) and 1 <= len(value) <= 19
            and value.isascii() and value.isdecimal()
            and (len(value) == 1 or value[0] != "0")
            and (not positive or value != "0") and int(value) <= maximum)


def _integer(value, maximum, *, positive=False):
    return type(value) is int and (1 if positive else 0) <= value <= maximum


def _payload(action, payload):
    keys = {"token", "expected_bot_id", "allowed_private_chat_ids"}
    if action == "telegram.poll":
        keys |= {"offset", "limit"}
    elif action != "telegram.probe":
        raise BridgeError("protocol_error")
    if not isinstance(payload, dict) or set(payload) != keys:
        raise BridgeError("protocol_error")
    token, bot_id, allowed = (payload["token"], payload["expected_bot_id"],
                              payload["allowed_private_chat_ids"])
    if (not isinstance(token, str) or not _TOKEN.fullmatch(token)
            or not _decimal(bot_id, _MAX_ID, positive=True)
            or token.split(":", 1)[0] != bot_id
            or not isinstance(allowed, list) or not 1 <= len(allowed) <= 20
            or any(not _decimal(peer, _MAX_ID, positive=True) for peer in allowed)
            or len(set(allowed)) != len(allowed)):
        raise BridgeError("invalid_config")
    if action == "telegram.poll" and (
            not _decimal(payload["offset"], _MAX_OFFSET)
            or not _integer(payload["limit"], 100, positive=True)):
        raise BridgeError("invalid_config")


def _event(update, allowed):
    """Return only a sanitized event and its minimal NoneBot model input."""
    update_id = update["update_id"]
    variants = [key for key in update if key != "update_id"]
    if len(variants) != 1:
        raise BridgeError("protocol_error")
    variant = variants[0]
    kind = variant if variant in _TYPES else "unknown"
    result = {"update_id": str(update_id), "type": kind, "reason": ""}
    model = {"update_id": update_id}

    def unsupported(reason):
        result["reason"] = reason
        return result, model

    if kind in _EDITS:
        return unsupported("unsupported_edit")
    if kind != "message":
        return unsupported("unsupported_event")
    message = update[variant]
    if not isinstance(message, dict) or not isinstance(message.get("chat"), dict):
        return unsupported("malformed_message")
    chat = message["chat"]
    if chat.get("type") in ("group", "supergroup", "channel"):
        return unsupported("not_private")
    if chat.get("type") != "private" or not _integer(chat.get("id"), _MAX_ID, positive=True):
        return unsupported("malformed_message")
    chat_id = str(chat["id"])
    if chat_id not in allowed:
        return unsupported("not_authorized")
    if any(message.get(key) is not None for key in _MEDIA):
        return unsupported("unsupported_media")
    if any(key not in _TEXT_FIELDS and value is not None for key, value in message.items()):
        return unsupported("unsupported_message")
    sender, text, date = message.get("from"), message.get("text"), message.get("date")
    if (not isinstance(sender, dict)
            or not _integer(sender.get("id"), _MAX_ID, positive=True)
            or sender["id"] != chat["id"] or type(sender.get("is_bot")) is not bool
            or not _integer(message.get("message_id"), _MAX_ID, positive=True)
            or not _integer(date, int(time.time()) + 86400, positive=True)
            or not isinstance(text, str) or not text or "\0" in text
            or len(text.encode("utf-8", "strict")) > _MAX_BODY):
        return unsupported("malformed_message")
    if sender["is_bot"]:
        return unsupported("unsupported_message")
    result["message"] = {"chat_id": chat_id, "message_id": str(message["message_id"]),
                         "sender_id": str(sender["id"]), "chat_type": "private",
                         "body": text, "date": date}
    # Keep the actual upstream Bot/TypeAdapter path, without instantiating any
    # unauthorized payload, reply history, contact name, or media object.
    model["message"] = {"message_id": message["message_id"], "chat": {
        "id": chat["id"], "type": "private"}, "from": {
        "id": sender["id"], "is_bot": False, "first_name": ""},
        "date": date, "text": text}
    return result, model


def _transport():
    # No environment proxy, redirect or automatic network retry, including 429.
    return httpx.AsyncHTTPTransport(retries=0)


def _platform_failure(status, value):
    code = status
    retry_after = 0
    if isinstance(value, dict) and value.get("ok") is False:
        candidate = value.get("error_code")
        if _integer(candidate, 2147483647, positive=True):
            code = candidate
        parameters = value.get("parameters")
        if isinstance(parameters, dict) and _integer(parameters.get("retry_after"), 86400):
            retry_after = parameters["retry_after"]
    if code == 401 or status == 401:
        kind = "auth_failed"
    elif code == 403 or status == 403:
        kind = "permission_denied"
    elif code == 429 or status == 429:
        kind = "rate_limited"
    elif status >= 500:
        kind = "unreachable"
    elif 400 <= code < 500:
        kind = "platform_rejected"
    else:
        kind = "protocol_error"
    raise BridgeError(kind, retry_after_seconds=retry_after if kind == "rate_limited" else 0,
                      platform_code=code if _integer(code, 2147483647) else 0)


class _Driver(HTTPXDriver):
    """A real NoneBot driver, limited to one fixed three-request sequence."""

    def __init__(self, action, payload, client):
        # model_construct deliberately bypasses all BaseSettings/.env/env
        # sources. NoneBot.init(), register_adapter() and Driver.run() are absent.
        super().__init__(Env.model_construct(environment="prod"),
                         NoneBotConfig.model_construct())
        self._payload = payload
        self._client = client
        self._sequence = ["getMe", "getWebhookInfo"]
        if action == "telegram.poll":
            self._sequence.append("getUpdates")
        self._position = 0
        self.events = None
        self.next_offset = None
        self.failure = None

    def __repr__(self):
        return "TelegramBridgeDriver(redacted)"

    async def request(self, setup):
        try:
            return await self._request(setup)
        except BridgeError as failure:
            # Upstream wraps driver exceptions in NetworkError. Remember only
            # our already-sanitized diagnostic, never its exception chain.
            self.failure = failure
            raise

    async def _request(self, setup):
        if self._position >= len(self._sequence):
            raise BridgeError("protocol_error")
        api = self._sequence[self._position]
        expected = "https://api.telegram.org/bot" + self._payload["token"] + "/" + api
        if (setup.method != "POST" or str(setup.url) != expected or setup.proxy is not None
                or setup.files or setup.data or setup.content
                or not isinstance(setup.json, dict)):
            raise BridgeError("protocol_error")
        params = {}
        if api == "getUpdates":
            params = {"offset": int(self._payload["offset"]),
                      "limit": self._payload["limit"], "timeout": 0, "allowed_updates": []}
        # The upstream adapter serializes each JSON value to a string. Validate
        # its exact output before restoring the typed Bot API parameter values.
        if set(setup.json) != set(params) or any(
                not isinstance(setup.json[key], str)
                or strict_loads(setup.json[key], 1024) != value
                or type(strict_loads(setup.json[key], 1024)) is not type(value)
                for key, value in params.items()):
            raise BridgeError("protocol_error")
        self._position += 1  # reserve before I/O: no exception can replay a call
        try:
            async with self._client.stream("POST", expected, json=params,
                                           headers={"Accept": "application/json"}) as response:
                body = bytearray()
                async for chunk in response.aiter_bytes():
                    if len(body) + len(chunk) > _MAX_RESPONSE:
                        raise BridgeError("protocol_error")
                    body.extend(chunk)
                status = response.status_code
        except BridgeError:
            raise
        except asyncio.CancelledError:
            raise BridgeError("canceled") from None
        except Exception:
            raise BridgeError("unreachable") from None
        value = strict_loads(bytes(body), _MAX_RESPONSE)
        if not isinstance(value, dict) or type(value.get("ok")) is not bool:
            raise BridgeError("protocol_error")
        if not 200 <= status < 300 or not value["ok"]:
            _platform_failure(status, value)
        if set(value) != {"ok", "result"}:
            raise BridgeError("protocol_error")
        result = value["result"]
        if api == "getMe":
            if (not isinstance(result, dict) or not _integer(result.get("id"), _MAX_ID, positive=True)
                    or type(result.get("is_bot")) is not bool):
                raise BridgeError("protocol_error")
            if str(result["id"]) != self._payload["expected_bot_id"] or not result["is_bot"]:
                raise BridgeError("identity_mismatch")
            if not isinstance(result.get("first_name"), str):
                raise BridgeError("protocol_error")
            result = {"id": result["id"], "is_bot": True, "first_name": ""}
        elif api == "getWebhookInfo":
            if (not isinstance(result, dict) or not isinstance(result.get("url"), str)
                    or type(result.get("has_custom_certificate")) is not bool
                    or not _integer(result.get("pending_update_count"), _MAX_OFFSET)):
                raise BridgeError("protocol_error")
            if result["url"]:
                raise BridgeError("webhook_present")
            result = {"url": "", "has_custom_certificate": result["has_custom_certificate"],
                      "pending_update_count": result["pending_update_count"]}
        else:
            if not isinstance(result, list) or len(result) > self._payload["limit"]:
                raise BridgeError("protocol_error")
            events, models = [], []
            # Go's encrypted-store lease selects offset=0 before I/O when an
            # old ID sequence may have expired. Keep validation against this
            # actual request strict: no worker reset, second page or retry.
            minimum = int(self._payload["offset"])
            previous = minimum - 1
            for update in result:
                if (not isinstance(update, dict)
                        or not _integer(update.get("update_id"), _MAX_UPDATE_ID)
                        or update["update_id"] < minimum or update["update_id"] <= previous):
                    raise BridgeError("protocol_error")
                previous = update["update_id"]
                event, model = _event(update, self._payload["allowed_private_chat_ids"])
                events.append(event)
                models.append(model)
            self.events = events
            self.next_offset = str(previous + 1) if events else self._payload["offset"]
            result = models
        # Inherit Adapter._call_api and Bot.call_api unchanged. The adapter reads
        # this strict sanitized result, then the real Bot validates its API type.
        content = json.dumps({"ok": True, "result": result}, ensure_ascii=False,
                             allow_nan=False, separators=(",", ":")).encode("utf-8")
        return Response(200, content=content, request=setup)


class _ManualAdapter(Adapter):
    def setup(self):
        # Stock setup registers polling/webhook startup (including deleteWebhook).
        # Never execute that lifecycle, connect a bot, or dispatch an event.
        pass


async def handle(action: str, payload: dict) -> dict:
    _payload(action, payload)
    driver = None
    try:
        async with httpx.AsyncClient(transport=_transport(), trust_env=False,
                                     follow_redirects=False, max_redirects=0,
                                     timeout=httpx.Timeout(10.0, connect=5.0)) as client:
            driver = _Driver(action, payload, client)
            adapter = _ManualAdapter(driver)
            bot = Bot(adapter, payload["expected_bot_id"], config=BotConfig(token=payload["token"]))
            await bot.get_me()
            await bot.get_webhook_info()
            result = {"bot_id": payload["expected_bot_id"], "is_bot": True,
                      "webhook_present": False}
            if action == "telegram.poll":
                updates = await bot.get_updates(offset=int(payload["offset"]), limit=payload["limit"],
                                                timeout=0, allowed_updates=[])
                if (driver.events is None or len(updates) != len(driver.events)
                        or any(str(update.update_id) != event["update_id"]
                               for update, event in zip(updates, driver.events))):
                    raise BridgeError("protocol_error")
                result.update(events=driver.events, next_offset=driver.next_offset)
            if driver._position != len(driver._sequence) or adapter.tasks or adapter.bots:
                raise BridgeError("protocol_error")
            return result
    except BridgeError:
        raise
    except asyncio.CancelledError:
        raise BridgeError("canceled") from None
    except Exception:
        # Never surface a token-bearing URL, Pydantic input, name or chat body.
        if driver is not None and driver.failure is not None:
            raise driver.failure from None
        raise BridgeError("protocol_error") from None
