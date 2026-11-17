"""Offline checks. All credentials, entities and messages below are synthetic."""
from __future__ import annotations

import asyncio
import datetime as dt
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from types import ModuleType, SimpleNamespace
import unittest
from unittest.mock import AsyncMock, patch

spec = importlib.util.spec_from_file_location("telegram_user_worker_tested", Path(__file__).with_name("worker.py"))
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)


def credential():
    return {"api_id": 12345, "api_hash": "a" * 32, "session": "1" + "A" * 352,
            "account_id": "900", "proxy_port": 0}


class User:
    def __init__(self, ident, name="Friend", **flags):
        self.id, self.first_name, self.last_name = ident, name, None
        self.username = None
        self.bot = self.is_self = self.deleted = False
        self.__dict__.update(flags)


TYPES = SimpleNamespace(User=User)


def message(ident=1, sender=123, text="Hello", **fields):
    result = SimpleNamespace(id=ident, sender_id=sender, raw_text=text, action=None,
                             date=dt.datetime(2026, 11, 16, 8, 0, tzinfo=dt.timezone.utc))
    result.__dict__.update(fields)
    return result


class FakeClient:
    """There are intentionally no send, download, mark-read or search methods."""
    def __init__(self, peers=None, messages=None, me=None):
        self.me = me if me is not None else User(900, "Owner", is_self=True)
        self.peers = peers or []
        self.messages = messages or []
        self.calls = []

    async def connect(self):
        self.calls.append(("connect",))

    async def disconnect(self):
        self.calls.append(("disconnect",))

    async def get_me(self):
        self.calls.append(("get_me",))
        return self.me

    async def iter_dialogs(self, *, limit):
        self.calls.append(("dialogs", limit))
        for peer in self.peers[:limit]:
            yield SimpleNamespace(entity=peer)

    async def iter_messages(self, peer, *, limit):
        self.calls.append(("messages", peer.id, limit))
        for item in self.messages[:limit]:
            yield item


class ProtocolTests(unittest.TestCase):
    def assert_failure(self, code, function, *args, **kwargs):
        with self.assertRaises(worker.Failure) as context:
            function(*args, **kwargs)
        self.assertEqual(context.exception.code, code)

    def test_only_explicit_actions_and_fields(self):
        bad = [b"", b"null", b"[]", b"{}", b'{"action":"send"}',
               b'{"action":null}', b'{"action":[]}', b'{"action":{}}',
               b'{"action":"probe","token":"secret"}',
               b'{"action":"probe","action":"read"}',
               b'{"action":"dialogs","limit":NaN}', b'{"action":"probe"}\n{}',
               b"\xff", b" " * 8193]
        for raw in bad:
            with self.subTest(raw=raw[:50]):
                self.assert_failure("INVALID_INPUT", worker.parse_request, raw)
        self.assertEqual(worker.parse_request(b'{"action":"probe"}'), {"action": "probe"})
        self.assertEqual(worker.parse_request(b'{"action":"dialogs"}')["limit"], 100)
        self.assertEqual(worker.parse_request(b'{"action":"read","peer_id":"123"}')["limit"], 50)

    def test_peer_and_limits_are_strict_before_network(self):
        for peer in [None, 123, True, "0", "-1", "01", "+12", " 12", "1.0", "１２", "9223372036854775808"]:
            raw = json.dumps({"action": "read", "peer_id": peer}).encode()
            self.assert_failure("INVALID_INPUT", worker.parse_request, raw)
        for limit in [None, True, "10", 1.5, 0, -1, 101]:
            self.assert_failure("INVALID_INPUT", worker.parse_request,
                                json.dumps({"action": "dialogs", "limit": limit}).encode())
        self.assertEqual(worker.parse_request(b'{"action":"read","peer_id":"9223372036854775807","limit":100}')["limit"], 100)

    def test_credentials_strict_shape_and_types(self):
        self.assertEqual(worker.validate_credentials(credential()), credential())
        cases = [{**credential(), "extra": "no"}, {**credential(), "api_id": True},
                 {**credential(), "api_hash": "secret"}, {**credential(), "session": "short"},
                 {**credential(), "account_id": 900}, {**credential(), "proxy_port": "1080"},
                 {**credential(), "proxy_port": 65536}]
        for case in cases:
            self.assert_failure("CONFIG_INVALID", worker.validate_credentials, case)

    def test_subprocess_invalid_action_single_redacted_json(self):
        result = subprocess.run([sys.executable, "-I", str(Path(worker.__file__))],
                                input=b'{"action":"send","session":"do-not-echo"}',
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10, check=True)
        self.assertEqual(result.stderr, b"")
        self.assertEqual(json.loads(result.stdout), {"ok": False, "error": {"code": "INVALID_INPUT"}})
        self.assertEqual(len(result.stdout.splitlines()), 1)
        self.assertNotIn(b"do-not-echo", result.stdout)

    def test_missing_credentials_without_network(self):
        with patch.object(worker, "validate_local_path", side_effect=worker.Failure("CONFIG_MISSING")):
            self.assert_failure("NOT_CONFIGURED", worker.load_credentials)

    def test_fixed_redacted_error_mapping(self):
        for name, expected in [("AuthKeyUnregisteredError", "AUTH_REQUIRED"),
                               ("SessionRevokedError", "AUTH_REQUIRED"),
                               ("ApiIdInvalidError", "API_INVALID"),
                               ("PhoneCodeInvalidError", "LOGIN_INVALID")]:
            exception = type(name, (Exception,), {})("super-secret")
            self.assertEqual(worker.error_result(exception), {"ok": False, "error": {"code": expected}})
        for exception, expected in [(TimeoutError("secret"), "UNREACHABLE"),
                                    (ModuleNotFoundError("secret"), "DEPENDENCY_MISSING"),
                                    (RuntimeError("super-secret"), "UNAVAILABLE")]:
            self.assertEqual(worker.error_result(exception)["error"]["code"], expected)
            self.assertNotIn("secret", json.dumps(worker.error_result(exception)))
        flood = type("FloodWaitError", (Exception,), {})("secret")
        flood.seconds = 45
        self.assertEqual(worker.error_result(flood), {"ok": False, "error": {"code": "FLOOD_WAIT", "retry_after": 45}})
        flood.seconds = 10**10
        self.assertEqual(worker.error_result(flood)["error"]["retry_after"], 604800)


class NormalizationTests(unittest.TestCase):
    def test_text_metadata_and_direction(self):
        incoming = worker.normalize_message(message(text="你好，Telegram"), "900", "123")
        self.assertEqual(incoming, {"external_id": "telegram:user:123:1", "body": "你好，Telegram",
                                   "direction": "incoming", "occurred_at": "2026-11-16T08:00:00Z"})
        outgoing = worker.normalize_message(message(sender=900), "900", "123")
        self.assertEqual(outgoing["direction"], "outgoing")

    def test_unsupported_or_unscoped_messages_are_skipped(self):
        examples = [message(text=""), message(text=None), message(text="\x00secret"),
                    message(text="中" * 5334), message(action=object()), message(sender=None),
                    message(sender=456), message(ident=0), message(date=None),
                    message(date=dt.datetime(2026, 1, 1)),
                    message(date=dt.datetime(1960, 1, 1, tzinfo=dt.timezone.utc)),
                    message(date=dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=2))]
        for item in examples:
            self.assertIsNone(worker.normalize_message(item, "900", "123"))

    def test_labels_bounded_and_no_phone_field(self):
        entity = User(123, "Name\x00\n" + "x" * 200, phone="private-phone")
        self.assertEqual(len(worker.label(entity)), 128)
        self.assertNotIn("private-phone", worker.label(entity))
        self.assertNotIn("\n", worker.label(entity))
        self.assertEqual(worker.label(User(123, "", username="public_name")), "public_name")


class ReadOnlyTests(unittest.IsolatedAsyncioTestCase):
    async def test_probe_only_checks_own_identity(self):
        client = FakeClient()
        result = await worker.perform({"action": "probe"}, credential(), client, TYPES)
        self.assertEqual(result, {"account_id": "900", "name": "Owner", "read_only": True, "sending_enabled": False})
        self.assertEqual(client.calls, [("connect",), ("get_me",), ("disconnect",)])

    async def test_identity_mismatch_and_bots_fail_closed(self):
        for me in [User(901), User(900, bot=True)]:
            client = FakeClient(me=me)
            with self.assertRaises(worker.Failure) as caught:
                await worker.perform({"action": "probe"}, credential(), client, TYPES)
            self.assertEqual(caught.exception.code, "IDENTITY_MISMATCH")
            self.assertEqual(client.calls[-1], ("disconnect",))
            self.assertFalse(any(call[0] == "dialogs" for call in client.calls))

    async def test_private_dialogs_exclude_groups_bots_self_deleted(self):
        peers = [User(123, "Friend"), SimpleNamespace(id=-10, title="Group"),
                 User(124, bot=True), User(125, is_self=True), User(126, deleted=True), User(127, "Other")]
        client = FakeClient(peers)
        result = await worker.perform({"action": "dialogs", "limit": 100}, credential(), client, TYPES)
        self.assertEqual(result["items"], [{"peer_id": "123", "name": "Friend"}, {"peer_id": "127", "name": "Other"}])
        self.assertFalse(result["limited"])

    async def test_dialog_window_is_bounded(self):
        client = FakeClient([User(ident) for ident in range(1, 103)])
        result = await worker.perform({"action": "dialogs", "limit": 100}, credential(), client, TYPES)
        self.assertEqual(len(result["items"]), 100)
        self.assertTrue(result["limited"])
        self.assertIn(("dialogs", 101), client.calls)

    async def test_read_only_selected_visible_private_peer(self):
        client = FakeClient([User(123)], [message(3), message(2, text=""), message(1, sender=900), message(4)])
        result = await worker.perform({"action": "read", "peer_id": "123", "limit": 3}, credential(), client, TYPES)
        self.assertEqual([item["external_id"] for item in result["messages"]], ["telegram:user:123:1", "telegram:user:123:3"])
        self.assertEqual(result["skipped"], 1)
        self.assertTrue(result["limited"])
        self.assertEqual(client.calls, [("connect",), ("get_me",), ("dialogs", 101), ("messages", 123, 4), ("disconnect",)])

    async def test_hidden_and_bot_peers_cannot_be_resolved_or_read(self):
        for peers, requested in [([User(123, bot=True)], "123"),
                                 ([User(ident) for ident in range(1, 102)], "101"),
                                 ([], "123")]:
            client = FakeClient(peers)
            with self.assertRaises(worker.Failure) as caught:
                await worker.perform({"action": "read", "peer_id": requested, "limit": 10}, credential(), client, TYPES)
            self.assertEqual(caught.exception.code, "PEER_NOT_AVAILABLE")
            self.assertFalse(any(call[0] == "messages" for call in client.calls))
            self.assertEqual(client.calls[-1], ("disconnect",))

    async def test_disconnect_on_network_failure_and_no_retry(self):
        class FailingClient(FakeClient):
            async def get_me(self):
                self.calls.append(("get_me",))
                raise ConnectionError("secret-network-location")
        client = FailingClient()
        with self.assertRaises(ConnectionError):
            await worker.perform({"action": "probe"}, credential(), client, TYPES)
        self.assertEqual(client.calls, [("connect",), ("get_me",), ("disconnect",)])


class ConstructionTests(unittest.TestCase):
    def test_memory_session_and_transport_defaults(self):
        calls = []
        telethon, sessions = ModuleType("telethon"), ModuleType("telethon.sessions")
        sessions.StringSession = lambda session="": ("in-memory-session", session)
        telethon.TelegramClient = lambda *args, **kwargs: calls.append((args, kwargs)) or object()
        with patch.dict(sys.modules, {"telethon": telethon, "telethon.sessions": sessions}):
            worker.new_client(credential())
            worker.new_client({**credential(), "proxy_port": 1080})
        args, options = calls[0]
        self.assertEqual(args[0], ("in-memory-session", credential()["session"]))
        self.assertFalse(options["receive_updates"])
        self.assertFalse(options["auto_reconnect"])
        self.assertEqual(options["connection_retries"], 0)
        self.assertEqual(options["request_retries"], 0)
        self.assertEqual(options["flood_sleep_threshold"], 0)
        self.assertTrue(options["raise_last_call_error"])
        self.assertEqual(options["timeout"], 10)
        self.assertIsNone(options["proxy"])
        self.assertEqual(calls[1][1]["proxy"], {"proxy_type": "socks5", "addr": "127.0.0.1", "port": 1080, "rdns": True})


class InitializationTests(unittest.IsolatedAsyncioTestCase):
    async def run_initialize(self, answer="SAVE", require_password=False, save_failure=False, logout_result=True, migrate=None):
        class SessionPasswordNeededError(Exception):
            pass

        class PhoneMigrateError(Exception):
            pass

        class LoginClient(FakeClient):
            def __init__(self):
                super().__init__()
                self.session = SimpleNamespace(save=lambda: credential()["session"])
                self.migrated = False

            def migration_once(self, stage):
                if migrate == stage and not self.migrated:
                    self.migrated = True
                    raise PhoneMigrateError()

            async def send_code_request(self, phone):
                self.calls.append(("login_code",))
                self.migration_once("code_request")
                return SimpleNamespace(phone_code_hash="synthetic-code-hash")

            async def sign_in(self, **kwargs):
                self.calls.append(("sign_in_password" if "password" in kwargs else "sign_in_code",))
                self.migration_once("password" if "password" in kwargs else "sign_in")
                if require_password and "password" not in kwargs:
                    raise SessionPasswordNeededError()

            async def log_out(self):
                self.calls.append(("log_out",))
                return logout_result

        class LocalTerminal(io.StringIO):
            def isatty(self):
                return True

        client, output = LoginClient(), LocalTerminal()
        telethon, sessions, errors = ModuleType("telethon"), ModuleType("telethon.sessions"), ModuleType("telethon.errors")
        telethon.TelegramClient = lambda *args, **kwargs: client
        sessions.StringSession = lambda: "in-memory-empty"
        errors.SessionPasswordNeededError = SessionPasswordNeededError
        errors.PhoneMigrateError = errors.NetworkMigrateError = errors.UserMigrateError = PhoneMigrateError
        hidden = [credential()["api_hash"], "+12345678901", "12345"]
        if require_password:
            hidden.append("synthetic-private-password")
        path = SimpleNamespace(parent="synthetic-private-directory", exists=lambda: False)
        with patch.dict(sys.modules, {"telethon": telethon, "telethon.sessions": sessions, "telethon.errors": errors}), \
             patch.object(worker.os, "name", "nt"), \
             patch.object(worker, "CREDENTIAL_PATH", path), \
             patch.object(worker, "validate_local_path"), patch.object(worker, "validate_private_acl"), \
             patch.object(worker, "save_credentials", side_effect=worker.Failure("CONFIG_UNAVAILABLE") if save_failure else None) as save, \
             patch.object(worker.sys, "stdin", LocalTerminal()), patch.object(worker.sys, "stdout", output), \
             patch("builtins.input", side_effect=["12345", "", answer]), \
             patch.object(worker.getpass, "getpass", side_effect=hidden):
            try:
                await worker.initialize()
                failure = None
            except worker.Failure as caught:
                failure = caught.code
        return client, output.getvalue(), save, failure

    async def test_own_identity_confirmed_before_dpapi_save(self):
        client, output, save, failure = await self.run_initialize(require_password=True)
        self.assertIsNone(failure)
        self.assertEqual(save.call_args.args[0], credential())
        self.assertIn("Owner (account ID: 900)", output)
        self.assertNotIn("synthetic-private-password", output)
        self.assertNotIn(credential()["api_hash"], output)
        self.assertEqual(client.calls, [("connect",), ("login_code",), ("sign_in_code",),
                                        ("sign_in_password",), ("get_me",), ("disconnect",)])

    async def test_declined_or_failed_save_revokes_fresh_session(self):
        client, _, save, failure = await self.run_initialize(answer="NO")
        self.assertEqual(failure, "CANCELLED")
        save.assert_not_called()
        self.assertEqual(client.calls[-2:], [("log_out",), ("disconnect",)])
        client, _, _, failure = await self.run_initialize(save_failure=True)
        self.assertEqual(failure, "CONFIG_UNAVAILABLE")
        self.assertEqual(client.calls[-2:], [("log_out",), ("disconnect",)])

    async def test_initialization_refuses_redirected_terminal(self):
        with patch.object(worker.os, "name", "nt"), patch.object(worker.sys, "stdin", io.StringIO("")), \
             patch.object(worker, "validate_local_path") as local_path:
            with self.assertRaises(worker.Failure) as caught:
                await worker.initialize()
        self.assertEqual(caught.exception.code, "INVALID_INPUT")
        local_path.assert_not_called()

    async def test_unsaved_login_revocation_false_is_visible(self):
        error_output = io.StringIO()
        with patch.object(worker.sys, "stderr", error_output):
            client, _, save, failure = await self.run_initialize(answer="NO", logout_result=False)
        self.assertEqual(failure, "CANCELLED")
        self.assertIn("Review Telegram Settings > Devices", error_output.getvalue())
        save.assert_not_called()
        self.assertEqual(client.calls[-2:], [("log_out",), ("disconnect",)])

    async def test_login_migration_only_one_retry_at_each_authentication_stage(self):
        for stage in ("code_request", "sign_in", "password"):
            with self.subTest(stage=stage):
                client, _, save, failure = await self.run_initialize(require_password=True, migrate=stage)
                self.assertIsNone(failure)
                self.assertTrue(client.migrated)
                self.assertEqual(save.call_count, 1)

    async def test_migration_retry_is_bounded_and_never_retries_flood_or_network(self):
        class Migration(Exception):
            pass
        operation = AsyncMock(side_effect=[Migration(), Migration()])
        with self.assertRaises(Migration):
            await worker.login_request(operation, (Migration,))
        self.assertEqual(operation.call_count, 2)
        for exception in (ConnectionError("secret"), type("FloodWaitError", (Exception,), {})("secret")):
            operation = AsyncMock(side_effect=exception)
            with self.assertRaises(type(exception)):
                await worker.login_request(operation, (Migration,))
            self.assertEqual(operation.call_count, 1)


@unittest.skipUnless(os.name == "nt", "Windows CurrentUser DPAPI / DACL checks")
class WindowsCredentialTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.pwsh = shutil.which("pwsh.exe")
        if not cls.pwsh:
            raise unittest.SkipTest("PowerShell 7 is required for the synthetic private-directory fixture")
        runtime = worker.PROJECT_ROOT / ".runtime"
        runtime.mkdir(exist_ok=True)
        cls.temporary = tempfile.TemporaryDirectory(prefix="telegram-tests-", dir=runtime)
        cls.root = Path(cls.temporary.name)
        cls.private = cls.root / "private"
        command = ("$ErrorActionPreference='Stop'; "
                   "Import-Module (Join-Path (Get-Location) 'scripts/WechatInit.psm1') -Force; "
                   "Initialize-WechatPrivateDirectory -Path $env:TELEGRAM_TEST_DIRECTORY | Out-Null")
        env = {**os.environ, "TELEGRAM_TEST_DIRECTORY": str(cls.private)}
        subprocess.run([cls.pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command],
                       cwd=worker.PROJECT_ROOT, env=env, check=True, capture_output=True, timeout=20)

    @classmethod
    def tearDownClass(cls):
        expected = (worker.PROJECT_ROOT / ".runtime").resolve()
        if not cls.root.resolve().is_relative_to(expected) or not cls.root.name.startswith("telegram-tests-"):
            raise RuntimeError("Refusing to remove a fixture outside the test workspace")
        cls.temporary.cleanup()

    def test_current_user_dpapi_roundtrip_and_wrong_entropy(self):
        plain = b"synthetic-session-test-no-real-account"
        encrypted = worker.dpapi(plain, True)
        self.assertNotIn(plain, encrypted)
        self.assertEqual(worker.dpapi(encrypted, False), plain)
        with patch.object(worker, "ENTROPY", b"different-product"):
            with self.assertRaises(worker.Failure) as caught:
                worker.dpapi(encrypted, False)
        self.assertEqual(caught.exception.code, "CONFIG_UNAVAILABLE")

    def test_credential_atomic_encrypted_roundtrip_and_explicit_replace(self):
        path = self.private / "synthetic-user.dpapi"
        worker.save_credentials(credential(), path=path)
        raw = path.read_bytes()
        self.assertNotIn(credential()["api_hash"].encode(), raw)
        self.assertNotIn(credential()["session"].encode(), raw)
        self.assertEqual(worker.load_credentials(path), credential())
        with self.assertRaises(worker.Failure) as caught:
            worker.save_credentials(credential(), path=path)
        self.assertEqual(caught.exception.code, "CONFIG_EXISTS")
        self.assertEqual(path.read_bytes(), raw)
        updated = {**credential(), "proxy_port": 1080}
        worker.save_credentials(updated, replace=True, path=path)
        self.assertEqual(worker.load_credentials(path), updated)
        self.assertEqual(list(self.private.glob("*.tmp")), [])

    def test_failed_atomic_replace_keeps_old_credential(self):
        path = self.private / "replace-failure.dpapi"
        worker.save_credentials(credential(), path=path)
        old = path.read_bytes()
        with patch.object(worker.os, "replace", side_effect=OSError("synthetic-failure")):
            with self.assertRaises(OSError):
                worker.save_credentials({**credential(), "proxy_port": 1080}, replace=True, path=path)
        self.assertEqual(path.read_bytes(), old)
        self.assertEqual(list(self.private.glob("*.tmp")), [])

    def test_unsafe_acl_missing_and_oversize_rejected(self):
        worker.validate_private_acl(self.private, True)
        with self.assertRaises(worker.Failure) as caught:
            worker.validate_private_acl(self.root, True)
        self.assertEqual(caught.exception.code, "CONFIG_UNSAFE")
        with self.assertRaises(worker.Failure) as caught:
            worker.load_credentials(self.private / "never-created.dpapi")
        self.assertEqual(caught.exception.code, "NOT_CONFIGURED")
        oversized = self.private / "oversized.dpapi"
        oversized.write_bytes(b"A" * 65537)
        with self.assertRaises(worker.Failure) as caught:
            worker.load_credentials(oversized)
        self.assertEqual(caught.exception.code, "CONFIG_INVALID")

    def test_local_path_rejects_unc_and_alternate_stream(self):
        for path in [Path(r"\\server\share\telegram.dpapi"), Path(str(self.private / "safe.dpapi") + ":extra")]:
            with self.assertRaises(worker.Failure) as caught:
                worker.validate_local_path(path, True)
            self.assertEqual(caught.exception.code, "CONFIG_UNSAFE")


if __name__ == "__main__":
    unittest.main()
