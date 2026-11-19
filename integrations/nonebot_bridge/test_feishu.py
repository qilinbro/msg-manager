"""Synthetic-only tests using the installed NoneBot/larkify/httpx2 packages.

Every request is intercepted by an in-memory transport. Unexpected real
network attempts are forbidden even if an implementation regression escapes
the operation gate. These checks do not assert real Feishu acceptance.
"""

import asyncio
import copy
import json
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import httpx2
from larkify.auth.token import TokenAuthStrategy
from nonebot.adapters.feishu import Adapter, Bot

from bridge_common import BridgeError, strict_loads
import feishu


APP_SECRET = "synthetic-feishu-app-secret-not-real"
TOKEN = "synthetic-feishu-access-token-not-real"
PAYLOAD = {"app_id": "cli_synthetic", "app_secret": APP_SECRET,
           "recipient_open_id": "ou_synthetic_owner", "text": '早报\nquote " slash \\ <>&\u2028',
           "uuid": "fd_synthetic_frozen_window_uuid"}


def token_body(**changes):
    result = {"code": 0, "access_token": TOKEN, "expires_in": 7200,
              "token_type": "Bearer", "scope": feishu.SEND_SCOPE}
    result.update(changes)
    return json.dumps(result).encode()


def receipt_body(message_id="om_synthetic_accepted"):
    return json.dumps({"code": 0, "data": {"message_id": message_id}}).encode()


def step(url, status=200, body=None, headers=None):
    return (url, status, body if body is not None else b"{}", headers or {})


class ScriptedTransport(httpx2.AsyncBaseTransport):
    def __init__(self, steps):
        self.steps = steps
        self.requests = []
        self.unexpected = []
        self.close_count = 0

    async def handle_async_request(self, request):
        self.requests.append(request)
        index = len(self.requests) - 1
        if index >= len(self.steps):
            self.unexpected.append("extra network attempt")
            raise AssertionError("extra synthetic network attempt")
        url, status, body, headers = self.steps[index]
        if str(request.url) != url:
            self.unexpected.append("unexpected destination")
            raise AssertionError("unexpected synthetic destination")
        if isinstance(body, BaseException):
            raise body
        if callable(body):
            body = await body(request)
        return httpx2.Response(status, headers={"Content-Type": "application/json", **headers},
                               stream=httpx2.ByteStream(body), request=request)

    async def aclose(self):
        self.close_count += 1


class FeishuPackageTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        forbidden = patch.object(httpx2.AsyncHTTPTransport, "handle_async_request",
                                 side_effect=AssertionError("REAL NETWORK IS FORBIDDEN"))
        forbidden.start()
        self.addCleanup(forbidden.stop)
        startup = patch("nonebot.init", side_effect=AssertionError("NoneBot startup is forbidden"))
        startup.start()
        self.addCleanup(startup.stop)

    async def run_steps(self, steps, payload=None, expected_error=None):
        transport = ScriptedTransport(steps)
        with patch.object(feishu, "_make_network_transport", return_value=transport):
            if expected_error is None:
                result = await feishu.handle("feishu.send", copy.deepcopy(PAYLOAD if payload is None else payload))
            else:
                with self.assertRaises(BridgeError) as caught:
                    await feishu.handle("feishu.send", copy.deepcopy(PAYLOAD if payload is None else payload))
                result = caught.exception
                self.assertEqual(result.kind, expected_error[0])
                self.assertEqual(result.send_state, expected_error[1])
                if len(expected_error) > 2:
                    self.assertEqual(result.platform_code, expected_error[2])
                for private in (APP_SECRET, TOKEN, PAYLOAD["recipient_open_id"], PAYLOAD["text"], "private-platform-msg"):
                    self.assertNotIn(private, str(result))
                    self.assertNotIn(private, repr(result))
        self.assertEqual(transport.unexpected, [])
        self.assertEqual(len(transport.requests), len(steps))
        self.assertEqual(transport.close_count, 1)
        return result, transport

    async def test_real_bot_adapter_and_sdk_api_with_exact_frozen_uuid(self):
        calls = []
        original_adapter_api = Adapter._call_api
        original_bot_api = Bot.call_api

        async def adapter_api(adapter, bot, api, **kwargs):
            calls.append(("adapter", api))
            self.assertIsInstance(adapter, Adapter)
            self.assertIsInstance(bot, Bot)
            self.assertIsNone(bot.bot_info)  # no discovery/identity claim
            self.assertIsInstance(bot.feishu.auth, TokenAuthStrategy)
            self.assertFalse(bot.feishu.config.auto_retry)
            self.assertFalse(bot.feishu.config.follow_redirects)
            self.assertFalse(bot.feishu.config.trust_env)
            self.assertIsNone(bot.feishu.config.proxy)
            return await original_adapter_api(adapter, bot, api, **kwargs)

        async def bot_api(bot, api, **kwargs):
            calls.append(("bot", api))
            return await original_bot_api(bot, api, **kwargs)

        async def forbidden_hook(*args, **kwargs):
            self.fail("ambient API hook must never run")

        with patch.object(Adapter, "_call_api", new=adapter_api), patch.object(Bot, "call_api", new=bot_api), \
                patch.object(Adapter, "__init__", side_effect=AssertionError("default adapter startup is forbidden")), \
                patch.object(Bot, "_calling_api_hook", {forbidden_hook}), patch.object(Bot, "_called_api_hook", {forbidden_hook}):
            result, transport = await self.run_steps([
                step(feishu.TOKEN_URL, body=token_body()),
                step(feishu.MESSAGE_URL, body=receipt_body()),
            ])
        self.assertEqual(calls, [("bot", "im/v1/messages"), ("adapter", "im/v1/messages")])
        self.assertEqual(result, {"message_id": "om_synthetic_accepted"})
        token_request, message_request = transport.requests
        self.assertEqual(dict(feishu.parse_qsl(token_request.content.decode())), {
            "grant_type": "client_credentials", "client_id": PAYLOAD["app_id"],
            "client_secret": APP_SECRET, "scope": feishu.SEND_SCOPE,
        })
        self.assertNotIn("authorization", token_request.headers)
        body = strict_loads(message_request.content)
        self.assertEqual(set(body), {"receive_id", "msg_type", "content", "uuid"})
        self.assertEqual(body["uuid"], PAYLOAD["uuid"])
        self.assertEqual(body["receive_id"], PAYLOAD["recipient_open_id"])
        self.assertEqual(body["msg_type"], "text")
        self.assertEqual(strict_loads(body["content"]), {"text": PAYLOAD["text"]})
        self.assertEqual(message_request.headers["authorization"], "Bearer " + TOKEN)
        for request in transport.requests:
            self.assertEqual(request.method, "POST")
            self.assertEqual(request.headers["accept-encoding"], "identity")
            self.assertNotIn("cookie", request.headers)

    async def test_policy_21001_and_other_token_errors_never_send_or_fallback(self):
        for status, code, kind in ((200, 21001, "permission_denied"), (400, 21001, "permission_denied"),
                                   (403, 21001, "permission_denied"), (200, 20068, "permission_denied"),
                                   (200, 20002, "auth_failed"), (429, 99991400, "rate_limited"),
                                   (200, 99991403, "rate_limited")):
            with self.subTest(status=status, code=code):
                await self.run_steps([step(feishu.TOKEN_URL, status, json.dumps({
                    "code": code, "msg": "private-platform-msg " + APP_SECRET, "log_id": "private-log"
                }).encode())], expected_error=(kind, "not_sent", code))

    async def test_token_schema_scope_size_and_receipt_are_strict(self):
        protocol_bodies = [
            b"{}", b"null", b"[]", b'{"code":false}', b'{"code":"0"}', b'{"code":0.0}',
            b'{"code":0,"code":0}', b'{"CODE":0}', b'{"code":0} {}',
            token_body() + b"\n" + b"x" * feishu.MAX_RESPONSE_BYTES,
            b'{"code":0,"extra":{"x":1,"x":2}}', b'{"code":NaN}',
            b'{"code":0,"extra":"\\ud800"}',
        ]
        unavailable_bodies = [
            b'{"code":0,"tenant_access_token":"old-token","expire":7200}',
            token_body(access_token=""), token_body(access_token="unsafe\r\nheader"),
            token_body(access_token="T" * (feishu.MAX_TOKEN_BYTES + 1)),
            token_body(expires_in=0), token_body(expires_in=-1), token_body(expires_in=86401),
            token_body(expires_in=True), token_body(expires_in=7200.0), token_body(token_type="Basic"),
        ]
        cases = [(body, "protocol_error") for body in protocol_bodies]
        cases += [(body, "unavailable") for body in unavailable_bodies]
        for body, kind in cases:
            with self.subTest(body_size=len(body), kind=kind):
                _, transport = await self.run_steps([step(feishu.TOKEN_URL, body=body)],
                                                    expected_error=(kind, "not_sent"))
                self.assertEqual(len(transport.requests), 1)
        for scope in (None, "im:message", feishu.SEND_SCOPE + " im:message"):
            await self.run_steps([step(feishu.TOKEN_URL, body=token_body(scope=scope))],
                                 expected_error=("permission_denied", "not_sent"))

    async def test_token_redirect_5xx_elapsed_and_reflected_token_have_no_send(self):
        for status in (302, 429, 500):
            await self.run_steps([step(feishu.TOKEN_URL, status, token_body(), {"Location": "https://evil.invalid/token"})],
                                 expected_error=("unavailable", "not_sent"))
        for clock in ([0, 8000], [0, 0, 8000]):
            # Expired while receiving OAuth OR before the message: neither
            # permits a refresh or a message attempt.
            with patch.object(feishu, "time", SimpleNamespace(monotonic=unittest.mock.Mock(side_effect=clock))):
                await self.run_steps([step(feishu.TOKEN_URL, body=token_body())], expected_error=("unavailable", "not_sent"))
        payload = dict(PAYLOAD, text="do not reflect " + TOKEN)
        await self.run_steps([step(feishu.TOKEN_URL, body=token_body())], payload=payload,
                             expected_error=("protocol_error", "not_sent"))

    async def test_definite_message_rejections_are_fixed_and_never_retried(self):
        for status, code, kind in ((200, 99991665, "auth_failed"), (401, 99991665, "auth_failed"),
                                   (400, 99991663, "auth_failed"), (403, 230006, "permission_denied"),
                                   (200, 230013, "platform_rejected"), (400, 230001, "platform_rejected"),
                                   (429, 99991400, "rate_limited"), (200, 230020, "rate_limited"),
                                   (200, 99991403, "rate_limited")):
            with self.subTest(status=status, code=code):
                await self.run_steps([step(feishu.TOKEN_URL, body=token_body()),
                                      step(feishu.MESSAGE_URL, status, json.dumps({"code": code, "msg": "private-platform-msg"}).encode(),
                                           {"Retry-After": "0"})], expected_error=(kind, "not_sent", code))

    async def test_message_unknown_matrix_never_becomes_success_or_retries(self):
        cases = [(400, receipt_body()), (429, receipt_body()), (500, receipt_body()), (302, receipt_body()),
                 (200, b"{}"), (200, b"null"), (200, b'{"code":"0"}'), (200, b'{"code":false}'),
                 (200, b'{"code":0,"code":0}'), (200, b'{"code":0} {}'),
                 (200, b'{"code":0,"data":{}}'), (200, b'{"code":0,"data":{"message_id":null}}'),
                 (200, b'{"code":0,"data":{"message_id":2}}'), (200, receipt_body("")),
                 (200, receipt_body("unsafe\nreceipt")), (200, receipt_body("x" * 257)),
                 (200, receipt_body(APP_SECRET)), (200, receipt_body(TOKEN)),
                 (200, receipt_body(PAYLOAD["recipient_open_id"])),
                 (200, b'{"code":99991665,"data":{"message_id":"om_contradictory"}}'),
                 (400, b'{"code":99991665,"data":{"message_id":42}}'),
                 (500, b'{"code":99991665}'), (200, b'{"code":987654321}'),
                 (200, b'{"code":230049}'),
                 (200, receipt_body() + b" " * feishu.MAX_RESPONSE_BYTES)]
        for status, body in cases:
            with self.subTest(status=status, body_size=len(body)):
                await self.run_steps([step(feishu.TOKEN_URL, body=token_body()), step(feishu.MESSAGE_URL, status, body)],
                                     expected_error=("send_unknown", "unknown"))

    async def test_invalid_response_content_type_encoding_and_stream_bounds(self):
        for headers in ({"Content-Type": "text/html"}, {"Content-Encoding": "gzip"},
                        {"Content-Length": str(feishu.MAX_RESPONSE_BYTES + 1)}):
            await self.run_steps([step(feishu.TOKEN_URL, body=token_body(), headers=headers)],
                                 expected_error=("protocol_error", "not_sent"))
            await self.run_steps([step(feishu.TOKEN_URL, body=token_body()),
                                  step(feishu.MESSAGE_URL, body=receipt_body(), headers=headers)],
                                 expected_error=("send_unknown", "unknown"))

    async def test_network_errors_are_redacted_and_phase_aware(self):
        for error in (httpx2.ConnectError("private-platform-msg " + APP_SECRET),
                      httpx2.ReadTimeout("private-platform-msg " + TOKEN), asyncio.CancelledError()):
            kind = "canceled" if isinstance(error, asyncio.CancelledError) else "unavailable"
            await self.run_steps([step(feishu.TOKEN_URL, body=error)], expected_error=(kind, "not_sent"))
            kind = "canceled" if isinstance(error, asyncio.CancelledError) else "send_unknown"
            await self.run_steps([step(feishu.TOKEN_URL, body=token_body()), step(feishu.MESSAGE_URL, body=error)],
                                 expected_error=(kind, "unknown"))

    async def test_input_validation_precedes_any_transport_creation(self):
        invalid = [dict(PAYLOAD, api_base="https://evil.invalid"), dict(PAYLOAD, app_id="other"),
                   dict(PAYLOAD, app_secret="unsafe\nsecret"), dict(PAYLOAD, recipient_open_id="oc_group"),
                   dict(PAYLOAD, uuid="uuid?query"), dict(PAYLOAD, uuid="u" * 51), dict(PAYLOAD, text=APP_SECRET),
                   dict(PAYLOAD, text="\x00private"), dict(PAYLOAD, text="\ud800"), dict(PAYLOAD, text="\u007f"),
                   dict(PAYLOAD, text="x" * (feishu.MAX_TEXT_BYTES + 1)), dict(PAYLOAD, text="\\" * 6000),
                   dict(PAYLOAD, text="<" * 3000), dict(PAYLOAD, text=None), {}, None, []]
        with patch.object(feishu, "_make_network_transport", side_effect=AssertionError("input created a transport")) as create:
            for payload in invalid:
                with self.subTest(payload_type=type(payload).__name__):
                    with self.assertRaises(BridgeError) as caught:
                        await feishu.handle("feishu.send", payload)
                    self.assertEqual(caught.exception.send_state, "not_sent")
            with self.assertRaises(BridgeError):
                await feishu.handle("feishu.generic", PAYLOAD)
            create.assert_not_called()

    async def test_gate_blocks_unexpected_hosts_actions_mutation_and_replay(self):
        frozen = feishu._validate_payload("feishu.send", PAYLOAD)
        invalid_urls = ["http://accounts.feishu.cn/oauth/v3/token", "https://evil.invalid/oauth/v3/token",
                        feishu.TOKEN_URL + "?x=1", "https://open.feishu.cn/open-apis/bot/v3/info"]
        for url in invalid_urls:
            inner = ScriptedTransport([])
            gate = feishu._OperationTransport(frozen, inner)
            with self.assertRaises(BridgeError):
                await gate.handle_async_request(httpx2.Request("POST", url, data=frozen.token_form()))
            self.assertEqual(inner.requests, [])
        for body in (dict(frozen.body(), uuid="different"), dict(frozen.body(), receive_id="ou_other"),
                     dict(frozen.body(), content='{"text":"changed"}'), dict(frozen.body(), extra="unexpected")):
            inner = ScriptedTransport([])
            gate = feishu._OperationTransport(frozen, inner)
            gate.token_attempted, gate.token, gate.refresh_at = True, TOKEN, feishu.time.monotonic() + 3600
            with self.assertRaises(BridgeError):
                await gate.handle_async_request(httpx2.Request("POST", feishu.MESSAGE_URL, json=body,
                                                              headers={"Authorization": "Bearer " + TOKEN}))
            self.assertEqual(inner.requests, [])
        inner = ScriptedTransport([step(feishu.TOKEN_URL, body=token_body())])
        gate = feishu._OperationTransport(frozen, inner)
        await gate.handle_async_request(httpx2.Request("POST", feishu.TOKEN_URL, data=frozen.token_form()))
        with self.assertRaises(BridgeError):
            await gate.handle_async_request(httpx2.Request("POST", feishu.TOKEN_URL, data=frozen.token_form()))
        request = httpx2.Request("POST", feishu.MESSAGE_URL, json=frozen.body(), headers={"Authorization": "Bearer " + TOKEN})
        with self.assertRaises(BridgeError):
            await gate.handle_async_request(request)
        self.assertEqual(len(inner.requests), 1)  # terminal denial, no SDK recovery
        self.assertEqual(inner.unexpected, [])
        await gate.shutdown()
        inner = ScriptedTransport([step(feishu.TOKEN_URL, body=token_body()), step(feishu.MESSAGE_URL, body=receipt_body())])
        gate = feishu._OperationTransport(frozen, inner)
        await gate.handle_async_request(httpx2.Request("POST", feishu.TOKEN_URL, data=frozen.token_form()))
        await gate.handle_async_request(request)
        with self.assertRaises(BridgeError):
            await gate.handle_async_request(request)
        self.assertEqual(len(inner.requests), 2)
        self.assertEqual(inner.unexpected, [])
        await gate.shutdown()

    async def test_cancel_during_token_or_message_has_no_late_replay(self):
        for phase in ("token", "message"):
            entered = asyncio.Event()

            async def blocked(request):
                entered.set()
                await asyncio.Future()

            steps = ([step(feishu.TOKEN_URL, body=token_body())] if phase == "message" else [])
            steps.append(step(feishu.MESSAGE_URL if phase == "message" else feishu.TOKEN_URL, body=blocked))
            transport = ScriptedTransport(steps)
            with patch.object(feishu, "_make_network_transport", return_value=transport):
                task = asyncio.create_task(feishu.handle("feishu.send", dict(PAYLOAD)))
                try:
                    await asyncio.wait_for(entered.wait(), timeout=3)
                    task.cancel()
                    with self.assertRaises(BridgeError) as caught:
                        await task
                    self.assertEqual(caught.exception.kind, "canceled")
                    self.assertEqual(caught.exception.send_state, "unknown" if phase == "message" else "not_sent")
                finally:
                    if not task.done():
                        task.cancel()
                        await asyncio.gather(task, return_exceptions=True)
            self.assertEqual(len(transport.requests), len(steps))
            self.assertEqual(transport.unexpected, [])
            self.assertEqual(transport.close_count, 1)

    def test_production_transport_disables_proxy_retry_http2_and_keepalive(self):
        with patch.object(httpx2, "AsyncHTTPTransport") as create:
            feishu._make_network_transport()
        options = create.call_args.kwargs
        self.assertFalse(options["trust_env"])
        self.assertEqual(options["retries"], 0)
        self.assertTrue(options["http1"])
        self.assertFalse(options["http2"])
        self.assertEqual(options["limits"].max_connections, 1)
        self.assertEqual(options["limits"].max_keepalive_connections, 0)
        self.assertGreaterEqual(options["verify"].minimum_version, feishu.ssl.TLSVersion.TLSv1_2)


if __name__ == "__main__":
    unittest.main()
