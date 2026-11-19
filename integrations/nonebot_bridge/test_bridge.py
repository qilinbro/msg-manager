"""Dependency-free IPC tests plus offline worker entrypoint checks."""
import asyncio
import io
import json
import logging
import os
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch

import bridge
from bridge_common import BridgeError, strict_loads

PRIVATE = "synthetic-bridge-private-marker-no-platform-io"


class StrictJSONTests(unittest.TestCase):
    def test_ambiguous_non_utf8_nonfinite_and_deep_json_are_rejected(self):
        malformed = [b'{"x":1,"x":2}', b'{"x":1,"\\u0078":2}', b'{} {}',
                     b'\xff', b'\xef\xbb\xbf{}', b'{"x":"\\ud800"}',
                     b'{"x":"\\udfff"}', b'{"x":NaN}', b'{"x":Infinity}',
                     b'{"x":1e999}', ("[" * 40 + "0" + "]" * 40).encode()]
        for raw in malformed:
            with self.subTest(raw=raw[:40]), self.assertRaises(BridgeError) as caught:
                strict_loads(raw)
            self.assertEqual(caught.exception.kind, "protocol_error")

    def test_utf8_pairs_and_literal_escaped_backslash_are_preserved(self):
        self.assertEqual(strict_loads(b'{"x":"\\ud83d\\ude00"}'), {"x": "😀"})
        self.assertEqual(strict_loads('{"x":"中文😀"}'), {"x": "中文😀"})
        self.assertEqual(strict_loads(b'{"x":"\\\\ud800"}'), {"x": "\\ud800"})

    def test_size_and_type_bounds(self):
        for data in [b"{}", "{}", None, 7]:
            with self.subTest(data=data), self.assertRaises(BridgeError):
                strict_loads(data, 1)

    def test_error_metadata_is_fixed_and_does_not_retain_raw_exception(self):
        failure = BridgeError(PRIVATE, retry_after_seconds=True, platform_code=-1,
                              send_state=PRIVATE)
        self.assertEqual(failure.as_dict(), {"kind": "unavailable",
                         "retry_after_seconds": 0, "platform_code": 0,
                         "send_state": "unknown"})
        self.assertNotIn(PRIVATE, str(failure))
        self.assertNotIn(PRIVATE, repr(failure))


class DispatchTests(unittest.IsolatedAsyncioTestCase):
    async def test_only_exact_fixed_actions_and_shape_can_import(self):
        requests = [None, [], {}, {"version": True, "action": "telegram.probe", "payload": {}},
                    {"version": 1, "action": "telegram.send", "payload": {}},
                    {"version": 1, "action": "call_api", "payload": {}},
                    {"version": 1, "action": "telegram.probe", "payload": []},
                    {"version": 1, "action": "telegram.probe", "payload": {}, "plugin": PRIVATE}]
        with patch.object(bridge.importlib, "import_module") as loader:
            for request in requests:
                with self.subTest(request=request), self.assertRaises(BridgeError):
                    await bridge.dispatch(request)
            loader.assert_not_called()

    async def test_fixed_module_receives_one_operation(self):
        handle = AsyncMock(return_value={"value": "synthetic"})
        with patch.object(bridge.importlib, "import_module", return_value=SimpleNamespace(handle=handle)) as loader:
            value = await bridge.dispatch({"version": 1, "action": "telegram.probe", "payload": {}})
        self.assertEqual(value, {"value": "synthetic"})
        loader.assert_called_once_with("telegram")
        handle.assert_awaited_once_with("telegram.probe", {})

    async def test_import_failure_is_proven_before_send_and_sanitized(self):
        with patch.object(bridge.importlib, "import_module", side_effect=RuntimeError(PRIVATE)):
            with self.assertRaises(BridgeError) as caught:
                await bridge.dispatch({"version": 1, "action": "feishu.send", "payload": {}})
        self.assertEqual((caught.exception.kind, caught.exception.send_state), ("unavailable", "not_sent"))
        self.assertNotIn(PRIVATE, repr(caught.exception))

    async def test_unexpected_post_dispatch_send_failure_is_unknown(self):
        for action, kind, state in [("feishu.send", "send_unknown", "unknown"),
                                    ("telegram.poll", "protocol_error", "not_sent")]:
            handle = AsyncMock(side_effect=RuntimeError(PRIVATE))
            with patch.object(bridge.importlib, "import_module", return_value=SimpleNamespace(handle=handle)):
                with self.assertRaises(BridgeError) as caught:
                    await bridge.dispatch({"version": 1, "action": action, "payload": {}})
            self.assertEqual((caught.exception.kind, caught.exception.send_state), (kind, state))
            self.assertNotIn(PRIVATE, str(caught.exception))
            self.assertEqual(handle.await_count, 1)

    async def test_structured_platform_error_is_not_retried_or_reinterpreted(self):
        failure = BridgeError("permission_denied", platform_code=21001)
        handle = AsyncMock(side_effect=failure)
        with patch.object(bridge.importlib, "import_module", return_value=SimpleNamespace(handle=handle)):
            with self.assertRaises(BridgeError) as caught:
                await bridge.dispatch({"version": 1, "action": "feishu.send", "payload": {}})
        self.assertIs(caught.exception, failure)
        handle.assert_awaited_once()


class EntrypointTests(unittest.TestCase):
    def test_actual_isolated_entrypoint_rejects_bad_input_without_network(self):
        worker = str(Path(__file__).resolve().with_name("bridge.py"))
        environment = {key: os.environ[key] for key in ("SystemRoot", "WINDIR", "TEMP", "TMP") if key in os.environ}
        environment["PATH"] = str(Path(sys.executable).parent)
        # Even the syntactically valid actions below have empty payloads, which
        # adapters must reject before constructing a network operation.
        for raw in [b"[]", b"not-json-" + PRIVATE.encode(), b"x" * (bridge.MAX_REQUEST_BYTES + 1),
                    b'{"version":1,"action":"feishu.send","payload":{}}',
                    b'{"version":1,"action":"telegram.probe","payload":{}}']:
            with self.subTest(size=len(raw)):
                completed = subprocess.run([sys.executable, "-I", "-u", "-B", worker],
                                           input=raw, capture_output=True, check=True,
                                           timeout=15, env=environment)
                self.assertEqual(completed.stderr, b"")
                self.assertEqual(completed.stdout.count(b"\n"), 1)
                self.assertNotIn(PRIVATE.encode(), completed.stdout)
                response = strict_loads(completed.stdout)
                self.assertEqual(set(response), {"version", "ok", "error"})
                self.assertEqual(response["version"], 1)
                self.assertIs(response["ok"], False)
                self.assertIn(response["error"]["kind"], {"protocol_error", "invalid_config", "unavailable"})
                self.assertEqual(response["error"]["send_state"], "not_sent")

    def run_main_with_result(self, result):
        output = io.BytesIO()
        previous_logging = logging.root.manager.disable
        request = b'{"version":1,"action":"telegram.probe","payload":{}}'
        with patch.object(sys, "stdin", SimpleNamespace(buffer=io.BytesIO(request))), \
                patch.object(sys, "stdout", SimpleNamespace(buffer=output)), \
                patch.object(sys, "stderr", io.StringIO()), \
                patch.object(bridge, "dispatch", AsyncMock(return_value=result)):
            try:
                bridge.main()
            finally:
                logging.disable(previous_logging)
        return output.getvalue()

    def test_main_caps_response_and_never_emits_private_value(self):
        raw = self.run_main_with_result({"value": PRIVATE + "x" * bridge.MAX_RESPONSE_BYTES})
        response = strict_loads(raw)
        self.assertNotIn(PRIVATE.encode(), raw)
        self.assertLess(len(raw), bridge.MAX_RESPONSE_BYTES)
        self.assertFalse(response["ok"])
        self.assertEqual(response["error"]["send_state"], "unknown")

    def test_main_rejects_unencodable_result_conservatively(self):
        for value in [{"value": "\ud800"}, {"value": {PRIVATE}}, {"value": float("inf")}]:
            raw = self.run_main_with_result(value)
            response = strict_loads(raw)
            self.assertNotIn(PRIVATE.encode(), raw)
            self.assertFalse(response["ok"])
            self.assertEqual(response["error"]["kind"], "protocol_error")
            self.assertEqual(response["error"]["send_state"], "unknown")


if __name__ == "__main__":
    unittest.main()
