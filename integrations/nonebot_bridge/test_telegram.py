"""Offline tests of the installed NoneBot Adapter/Bot, not stand-in packages."""
import asyncio
import copy
import json
from pathlib import Path
import socket
import sys
import unittest
from unittest.mock import patch

# unittest discover and Python -I must not need PYTHONPATH or the current .env.
sys.path.insert(0, str(Path(__file__).resolve().parent))

import httpx
from nonebot.adapters.telegram import Adapter, Bot
from nonebot.config import Config as NoneBotConfig, Env
from nonebot.drivers import Request

from bridge_common import BridgeError
import telegram


BOT_ID = "100"
TOKEN = BOT_ID + ":synthetic_only_abcdefghijklmnopqrstuvwxyz"
PEER = "42"
NOW = 1767225600
TEXT = "  @synthetic_bot 原文保留\n\t尾部  "


def payload(poll=False, **changes):
    value = {"token": TOKEN, "expected_bot_id": BOT_ID,
             "allowed_private_chat_ids": [PEER]}
    if poll:
        value.update(offset="0", limit=20)
    value.update(changes)
    return value


def message(update_id=10, **changes):
    value = {"message_id": 7, "date": NOW, "chat": {"id": 42, "type": "private"},
             "from": {"id": 42, "is_bot": False, "first_name": "synthetic-private-name"},
             "text": TEXT, "entities": [{"type": "mention", "offset": 2, "length": 14}]}
    value.update(changes)
    return {"update_id": update_id, "message": value}


def ok(value):
    return httpx.Response(200, json={"ok": True, "result": value})


class TelegramTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.calls = []
        self.api_calls = []
        self.updates = []
        self.override = None
        self.client_options = []
        self.patches = []
        self.addCleanup(self._cleanup_patches)
        # Fail even if a bug bypasses MockTransport and creates another client.
        for name in ("connect", "connect_ex"):
            self._patch(patch.object(socket.socket, name, side_effect=AssertionError("network forbidden")))
        self._patch(patch.object(socket, "getaddrinfo", side_effect=AssertionError("DNS forbidden")))
        self._patch(patch.object(NoneBotConfig, "__init__", side_effect=AssertionError("settings forbidden")))
        self._patch(patch.object(Env, "__init__", side_effect=AssertionError("env forbidden")))
        self._patch(patch.object(Adapter, "setup", side_effect=AssertionError("lifecycle forbidden")))
        self._patch(patch.object(Bot, "handle_event", side_effect=AssertionError("event dispatch forbidden")))
        self._patch(patch.object(telegram._Driver, "run", side_effect=AssertionError("driver start forbidden")))
        self._patch(patch.object(telegram, "_transport", side_effect=lambda: httpx.MockTransport(self._request)))
        original_api = Adapter._call_api

        async def observe_api(adapter, bot, api, **data):
            self.api_calls.append((adapter, bot, api))
            return await original_api(adapter, bot, api, **data)

        self._patch(patch.object(Adapter, "_call_api", new=observe_api))
        original_client = httpx.AsyncClient

        def observe_client(**options):
            self.client_options.append(options)
            return original_client(**options)

        self._patch(patch.object(telegram.httpx, "AsyncClient", side_effect=observe_client))

    def _patch(self, manager):
        value = manager.start()
        self.patches.append(manager)
        return value

    def _cleanup_patches(self):
        for manager in reversed(self.patches):
            manager.stop()

    async def _request(self, request):
        self.assertEqual(request.method, "POST")
        self.assertEqual(request.url.scheme, "https")
        self.assertEqual(request.url.host, "api.telegram.org")
        self.assertFalse(request.url.query)
        api = request.url.path.rsplit("/", 1)[1]
        self.assertTrue(request.url.path.startswith("/bot" + TOKEN + "/"))
        params = json.loads(request.content)
        self.calls.append((api, params))
        if self.override is not None:
            value = self.override(api, params)
            if value is not None:
                return value
        if api == "getMe":
            self.assertEqual(params, {})
            return ok({"id": int(BOT_ID), "is_bot": True, "first_name": "synthetic bot"})
        if api == "getWebhookInfo":
            self.assertEqual(params, {})
            return ok({"url": "", "has_custom_certificate": False, "pending_update_count": 0})
        if api == "getUpdates":
            self.assertEqual(params["timeout"], 0)
            self.assertIs(type(params["timeout"]), int)
            self.assertEqual(params["allowed_updates"], [])
            return ok(self.updates)
        self.fail("unexpected Telegram API")

    async def failure(self, kind, action="telegram.poll", value=None):
        with self.assertRaises(BridgeError) as raised:
            await telegram.handle(action, value if value is not None else payload(action.endswith("poll")))
        failure = raised.exception
        self.assertEqual(failure.kind, kind)
        rendered = str(failure) + repr(failure) + json.dumps(failure.as_dict())
        for secret in (TOKEN, TEXT, "synthetic-private-name", "raw-sensitive-marker"):
            self.assertNotIn(secret, rendered)
        self.assertEqual(failure.send_state, "not_sent")
        return failure

    async def test_probe_uses_actual_adapter_bot_without_lifecycle_or_settings(self):
        result = await telegram.handle("telegram.probe", payload())
        self.assertEqual(result, {"bot_id": BOT_ID, "is_bot": True, "webhook_present": False})
        self.assertEqual([name for name, _ in self.calls], ["getMe", "getWebhookInfo"])
        self.assertEqual([name for _, _, name in self.api_calls], ["get_me", "get_webhook_info"])
        for adapter, bot, _ in self.api_calls:
            self.assertIsInstance(adapter, Adapter)
            self.assertIsInstance(bot, Bot)
            self.assertEqual(adapter.tasks, set())
            self.assertEqual(adapter.bots, {})
            self.assertEqual(adapter.driver.bots, {})
        for options in self.client_options:
            self.assertIs(options["trust_env"], False)
            self.assertIs(options["follow_redirects"], False)
            self.assertEqual(options["max_redirects"], 0)

    async def test_poll_is_one_page_preserves_mentions_and_original_whitespace(self):
        self.updates = [message()]
        result = await telegram.handle("telegram.poll", payload(True))
        self.assertEqual([name for name, _ in self.calls], ["getMe", "getWebhookInfo", "getUpdates"])
        self.assertEqual([name for _, _, name in self.api_calls],
                         ["get_me", "get_webhook_info", "get_updates"])
        self.assertEqual(self.calls[-1][1], {"offset": 0, "limit": 20, "timeout": 0, "allowed_updates": []})
        self.assertEqual(result["next_offset"], "11")
        self.assertEqual(result["events"], [{"update_id": "10", "type": "message", "reason": "",
                         "message": {"chat_id": "42", "message_id": "7", "sender_id": "42",
                                     "chat_type": "private", "body": TEXT, "date": NOW}}])
        self.assertNotIn("synthetic-private-name", json.dumps(result))

    async def test_idle_epoch_zero_reseek_accepts_low_and_reused_ids_without_retry(self):
        self.updates = [message(200000000), message(900000000, message_id=8)]
        old = await telegram.handle("telegram.poll", payload(True, offset="0"))
        self.assertEqual(old["next_offset"], "900000001")
        # Simulates a later random sequence, with a reused update ID but a new
        # stable private message. Go must choose zero BEFORE the one-page call;
        # the actual installed SDK still gets normal strict request validation.
        self.updates = [message(200000000, message_id=9)]
        await self.failure("protocol_error", value=payload(True, offset="900000001"))
        reset = await telegram.handle("telegram.poll", payload(True, offset="0"))
        self.assertEqual(reset["next_offset"], "200000001")
        self.assertEqual(reset["events"][0]["message"]["message_id"], "9")
        self.assertEqual([params["offset"] for name, params in self.calls if name == "getUpdates"],
                         [0, 900000001, 0])
        self.assertEqual(len(self.calls), 9)
        self.assertEqual([name for _, _, name in self.api_calls],
                         ["get_me", "get_webhook_info", "get_updates"] * 3)

    async def test_empty_page_does_not_advance_including_maximum_cursor(self):
        for offset in ("0", "123", str(telegram._MAX_OFFSET)):
            with self.subTest(offset=offset):
                result = await telegram.handle("telegram.poll", payload(True, offset=offset))
                self.assertEqual(result["next_offset"], offset)
                self.assertEqual(result["events"], [])
        self.assertEqual(len(self.calls), 9)

    async def test_large_ids_remain_exact_decimal_strings(self):
        max_id = telegram._MAX_ID
        self.updates = [message(telegram._MAX_UPDATE_ID, message_id=max_id,
                               chat={"id": max_id, "type": "private"},
                               **{"from": {"id": max_id, "is_bot": False}})]
        result = await telegram.handle("telegram.poll", payload(True, allowed_private_chat_ids=[str(max_id)]))
        event = result["events"][0]
        self.assertEqual(event["update_id"], str(telegram._MAX_UPDATE_ID))
        self.assertEqual(event["message"]["chat_id"], str(max_id))
        self.assertEqual(event["message"]["message_id"], str(max_id))
        self.assertEqual(result["next_offset"], str(telegram._MAX_OFFSET))

    async def test_unsupported_payloads_are_reduced_to_reason_before_nonebot_models(self):
        marker = "raw-sensitive-marker"
        self.updates = [
            message(0, chat={"id": -100, "type": "supergroup"}, text=marker),
            message(1, chat={"id": 99, "type": "private"}, text=marker),
            message(2, photo=[{"raw": marker}], caption=marker),
            {"update_id": 3, "edited_message": {"text": marker}},
            {"update_id": 4, "channel_post": {"text": marker}},
            {"update_id": 5, "callback_query": {"data": marker}},
            {"update_id": 6, "new_unrecognized_event": {"raw": marker}},
            message(7, text=None),
            message(8, unknown_service={"raw": marker}),
            {"update_id": 9, "message": None},
            message(10, **{"from": {"id": 43, "is_bot": False}}),
            message(11, **{"from": {"id": 42, "is_bot": True}}),
        ]
        result = await telegram.handle("telegram.poll", payload(True))
        self.assertEqual(result["next_offset"], "12")
        self.assertEqual([event["reason"] for event in result["events"]], [
            "not_private", "not_authorized", "unsupported_media", "unsupported_edit",
            "unsupported_event", "unsupported_event", "unsupported_event", "malformed_message",
            "unsupported_message", "malformed_message", "malformed_message", "unsupported_message"])
        self.assertNotIn(marker, json.dumps(result))
        self.assertTrue(all(set(event) == {"update_id", "type", "reason"} for event in result["events"]))

    async def test_every_known_nonmessage_type_is_minimal(self):
        for kind in sorted(telegram._TYPES - {"message"}):
            with self.subTest(kind=kind):
                self.updates = [{"update_id": 0, kind: {"raw": "raw-sensitive-marker"}}]
                result = await telegram.handle("telegram.poll", payload(True))
                expected = "unsupported_edit" if kind in telegram._EDITS else "unsupported_event"
                self.assertEqual(result["events"], [{"update_id": "0", "type": kind, "reason": expected}])

    async def test_malformed_pages_fail_without_acknowledging_any_part(self):
        cases = [None, {}, [message(True)], [message(-1)], [message("10")],
                 [message(telegram._MAX_OFFSET)], [message(2), message(1)],
                 [message(1), message(1)], [{"update_id": 0}],
                 [{"update_id": 0, "message": {}, "edited_message": {}}],
                 [message(i) for i in range(21)]]
        for updates in cases:
            with self.subTest(updates=updates):
                self.updates = updates
                await self.failure("protocol_error")
        self.updates = [message(1)]
        await self.failure("protocol_error", value=payload(True, offset="2"))

    async def test_malformed_private_text_is_only_an_unsupported_reason(self):
        changes = [{"message_id": True}, {"message_id": 0}, {"message_id": telegram._MAX_ID + 1},
                   {"date": True}, {"date": -1}, {"date": telegram._MAX_OFFSET},
                   {"text": ""}, {"text": "nul\0text"}, {"text": "长" * 5334},
                   {"chat": {"id": True, "type": "private"}},
                   {"chat": {"id": "42", "type": "private"}},
                   {"from": {"id": 42, "is_bot": 0}}, {"from": None}]
        for change in changes:
            with self.subTest(change=change):
                self.updates = [message(**change)]
                result = await telegram.handle("telegram.poll", payload(True))
                self.assertEqual(result["events"], [{"update_id": "10", "type": "message",
                                                     "reason": "malformed_message"}])

    async def test_identity_and_webhook_fail_closed_before_getupdates(self):
        for me, expected in (({"id": 101, "is_bot": True, "first_name": "x"}, "identity_mismatch"),
                             ({"id": 100, "is_bot": False, "first_name": "x"}, "identity_mismatch"),
                             ({"id": True, "is_bot": True, "first_name": "x"}, "protocol_error"),
                             ({"id": 100, "is_bot": 1, "first_name": "x"}, "protocol_error"),
                             ({"id": 100, "is_bot": True}, "protocol_error")):
            with self.subTest(me=me):
                self.calls.clear()
                self.override = lambda api, _, value=me: ok(value) if api == "getMe" else None
                await self.failure(expected)
                self.assertEqual([name for name, _ in self.calls], ["getMe"])
        for hook, expected in (({"url": "https://raw-sensitive-marker.invalid/token",
                                  "has_custom_certificate": False, "pending_update_count": 1}, "webhook_present"),
                               ({"url": None, "has_custom_certificate": False,
                                 "pending_update_count": 1}, "protocol_error"),
                               ({"url": "", "has_custom_certificate": "false",
                                 "pending_update_count": 1}, "protocol_error")):
            with self.subTest(hook=hook):
                self.calls.clear()
                self.override = lambda api, _, value=hook: ok(value) if api == "getWebhookInfo" else None
                await self.failure(expected)
                self.assertEqual([name for name, _ in self.calls], ["getMe", "getWebhookInfo"])

    async def test_auth_rate_limit_network_and_cancellation_never_retry(self):
        for status, code, kind in ((401, 401, "auth_failed"), (403, 403, "permission_denied"),
                                   (429, 429, "rate_limited"), (400, 400, "platform_rejected"),
                                   (500, 500, "unreachable"), (200, 401, "auth_failed"),
                                   (302, 302, "protocol_error")):
            with self.subTest(status=status, code=code):
                self.calls.clear()
                self.override = lambda api, _, s=status, c=code: httpx.Response(s, json={
                    "ok": False, "error_code": c, "description": TOKEN + TEXT,
                    "parameters": {"retry_after": 12}}, headers={"Location": "https://unexpected.invalid"})
                failure = await self.failure(kind)
                self.assertEqual(failure.retry_after_seconds, 12 if kind == "rate_limited" else 0)
                self.assertEqual(len(self.calls), 1)
        for raised, kind in ((httpx.ConnectError(TOKEN + TEXT), "unreachable"),
                              (httpx.ReadTimeout(TOKEN + TEXT), "unreachable"),
                              (asyncio.CancelledError(), "canceled")):
            self.calls.clear()

            def fail_request(api, params, error=raised):
                raise error

            self.override = fail_request
            await self.failure(kind)
            self.assertEqual(len(self.calls), 1)

    async def test_strict_response_json_and_byte_bounds(self):
        cases = [b'{"ok":true,"ok":true,"result":{}}', b'{"ok":true,"result":NaN}',
                 b'{"ok":true,"result":{}} {}', b'null', b'[]', b'\xff',
                 b'{"ok":"true","result":{}}', b'{"ok":true}',
                 b'{"ok":true,"result":{},"raw":"raw-sensitive-marker"}',
                 b'{"ok":true,"result":' + b'[' * 34 + b'0' + b']' * 34 + b'}',
                 b' ' * ((4 << 20) + 1)]
        for raw in cases:
            with self.subTest(prefix=raw[:60]):
                self.override = lambda api, _, data=raw: httpx.Response(200, content=data)
                await self.failure("protocol_error")

    async def test_strict_input_and_unknown_actions_make_no_network_call(self):
        cases = [payload(True, token=""), payload(True, token=TOKEN + "/sendMessage"),
                 payload(True, expected_bot_id="0100"), payload(True, expected_bot_id="101"),
                 payload(True, allowed_private_chat_ids=[]),
                 payload(True, allowed_private_chat_ids=["42", "42"]),
                 payload(True, allowed_private_chat_ids=["-42"]),
                 payload(True, allowed_private_chat_ids=["1"] * 21),
                 payload(True, offset="01"), payload(True, offset="-1"),
                 payload(True, offset=str(telegram._MAX_OFFSET + 1)),
                 payload(True, limit=True), payload(True, limit=0), payload(True, limit=101)]
        for value in cases:
            await self.failure("invalid_config", value=value)
        for value in ({}, payload(True, arbitrary_url="https://unexpected.invalid"),
                      {**payload(True), "token": None, "extra": None}):
            await self.failure("protocol_error", value=value)
        await self.failure("protocol_error", action="telegram.send", value=payload())
        await self.failure("protocol_error", action="telegram.probe", value=payload(True))
        self.assertEqual(self.calls, [])
        self.assertEqual(self.client_options, [])

    async def test_driver_itself_blocks_arbitrary_api_url_method_and_parameters(self):
        for request in (Request("POST", "https://api.telegram.org/bot" + TOKEN + "/deleteWebhook", json={}),
                        Request("POST", "http://api.telegram.org/bot" + TOKEN + "/getMe", json={}),
                        Request("POST", "https://unexpected.invalid/bot" + TOKEN + "/getMe", json={}),
                        Request("GET", "https://api.telegram.org/bot" + TOKEN + "/getMe", json={}),
                        Request("POST", "https://api.telegram.org/bot" + TOKEN + "/getMe", json={"extra": "1"})):
            driver = telegram._Driver("telegram.probe", payload(), None)
            with self.assertRaises(BridgeError):
                await driver.request(request)
        self.assertEqual(self.calls, [])

    async def test_repeated_manual_calls_never_keep_a_candidate_offset_in_memory(self):
        self.updates = [message()]
        first = await telegram.handle("telegram.poll", payload(True))
        second = await telegram.handle("telegram.poll", payload(True))
        self.assertEqual(first, second)
        self.assertEqual([params["offset"] for name, params in self.calls if name == "getUpdates"], [0, 0])


if __name__ == "__main__":
    unittest.main()
