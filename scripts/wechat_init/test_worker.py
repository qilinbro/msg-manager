"""Synthetic-only tests. No real process inspection or Frida attachment occurs."""
from __future__ import annotations

import base64
import contextlib
import io
import json
import os
from pathlib import Path
import shutil
import sys
import uuid
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from scripts.wechat_init import worker
from scripts.wechat_init.native import InitError
from scripts.wechat_init.native import Native
from third_party.flandora_wxkey import wx_crypto


def fixture():
    payload = json.loads((Path(__file__).parent / "testdata" / "synthetic_page.json").read_text("utf-8"))
    return bytes.fromhex(payload["password_hex"]), base64.b64decode(payload["page_base64"], validate=True)


class FakeNative:
    def __init__(self, root):
        key, page = fixture()
        self.key = key
        self.dll = root / "Weixin.dll"
        self.dll.write_bytes(b"synthetic PE stand-in; parser mocked")
        self.database = root / "session.db"
        self.database.write_bytes(page)
        self.output = root / "output.json"
        self.target = {"created": 123456, "exe": str(root / "Weixin.exe"), "dlls": [str(self.dll)], "sid": "S-1-5-21-synthetic", "current_sid": "S-1-5-21-synthetic"}
        self.inspections = []
        self.closed = False
        self.version_value = worker.CLIENT_VERSION
        self.saved = None

    def paths(self, args):
        return self.dll, self.database, self.output

    def inspect(self, pid):
        self.inspections.append(pid)
        return dict(self.target)

    def canonical(self, path):
        return Path(path)

    def version(self, path):
        return self.version_value

    def protect(self, raw):
        assert raw == self.key
        return b"synthetic DPAPI ciphertext, not the raw key"

    def write_output(self, path, payload):
        self.saved = payload

    def close(self):
        self.closed = True


class FakeCancellable:
    def __init__(self):
        self.cancelled = False

    def cancel(self):
        self.cancelled = True

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False


class FakeScript:
    def __init__(self, events):
        self.events = events
        self.callback = None
        self.unloaded = False

    def on(self, event, callback):
        assert event == "message"
        self.callback = callback

    def load(self):
        for event, data in self.events:
            self.callback(event, data)

    def unload(self):
        self.unloaded = True


class FakeSession:
    def __init__(self, script):
        self.script = script
        self.detached = False
        self.source = None
        self.callback = None

    def on(self, event, callback):
        assert event == "detached"
        self.callback = callback

    def create_script(self, source):
        self.source = source
        return self.script

    def detach(self):
        self.detached = True


def sent(event, **values):
    return {"type": "send", "payload": {"event": event, **values}}


class WorkerTests(unittest.TestCase):
    def setUp(self):
        # Python 3.13's Windows mkdir(0700) creates a new DACL which is not
        # usable by some restricted test tokens. Inherit this test directory's
        # ACL instead; fixtures here are public synthetic data only.
        root = Path(__file__).parent.resolve()
        child = root / (".synthetic-test-" + uuid.uuid4().hex)
        child.mkdir(mode=0o755)
        self.addCleanup(self.remove_fixture_dir, root, child)
        self.native = FakeNative(child)
        self.args = SimpleNamespace(pid=42001, created_filetime=123456, dll=str(self.native.dll), database=str(self.native.database), account_id="wxid_synthetic_test", consent_account="wxid_synthetic_test", output=str(self.native.output), timeout=10)

    @staticmethod
    def remove_fixture_dir(root, child):
        if child.parent != root or not child.name.startswith(".synthetic-test-") or child.is_symlink():
            raise AssertionError("unsafe synthetic fixture cleanup path")
        shutil.rmtree(child)

    def fake_frida(self, events=None):
        if events is None:
            events = [(sent("ready"), None), (sent("material", tag="deref0", size=32), self.native.key), (sent("call", count=1), None), (sent("done"), None)]
        script = FakeScript(events)
        session = FakeSession(script)
        attached = []

        def attach(pid):
            attached.append(pid)
            return session

        module = SimpleNamespace(Cancellable=FakeCancellable, attach=attach)
        return module, session, script, attached

    def run_worker(self, module, **options):
        notes = []
        with patch.object(worker.locator, "PeImage", side_effect=lambda p: SimpleNamespace(data=p.read_bytes())), patch.object(worker.locator, "scan_key_setter", return_value=(4096, [4000])):
            result = worker.execute(self.args, native=self.native, loader=lambda name: module, notify=lambda event: notes.append(event), **options)
        return result, notes

    def test_public_fixture_is_a_genuinely_encrypted_sqlcipher_page(self):
        key, page = fixture()
        self.assertEqual(len(page), 4096)
        self.assertFalse(page.startswith(b"SQLite format 3"))
        self.assertNotIn(b"wxid_public_fixture", page)
        self.assertTrue(wx_crypto.check_page1(key, page))
        self.assertFalse(wx_crypto.check_page1(bytes(reversed(key)), page))
        changed = bytearray(page)
        changed[80] ^= 1
        self.assertFalse(wx_crypto.check_page1(key, bytes(changed)))

    def test_success_single_pid_safe_events_encrypted_output_and_cleanup(self):
        module, session, script, attached = self.fake_frida()
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            result, events = self.run_worker(module)
        self.assertEqual(result, "initialized")
        self.assertEqual(events, ["ready", "waiting"])
        self.assertEqual(attached, [self.args.pid])
        self.assertEqual(set(self.native.inspections), {self.args.pid})
        self.assertTrue(session.detached and script.unloaded and self.native.closed)
        self.assertEqual(stdout.getvalue(), "")
        self.assertEqual(stderr.getvalue(), "")
        self.assertEqual(self.native.saved["account_id"], self.args.account_id)
        self.assertEqual(self.native.saved["format"], 1)
        serialized = json.dumps(self.native.saved)
        self.assertNotIn(self.native.key.hex(), serialized)
        self.assertNotIn(base64.b64encode(self.native.key).decode(), serialized)
        self.assertIn("Process.id !== pidExpected", session.source)
        self.assertNotIn("enumerate_process", session.source)
        self.assertFalse(wx_crypto._KEY_CACHE)

    def test_consent_is_required_before_any_native_operation_or_frida_import(self):
        self.args.consent_account = "wxid_different"
        with self.assertRaisesRegex(InitError, "consent_required"), patch.object(self.native, "paths") as paths:
            worker.execute(self.args, native=self.native, loader=lambda _: self.fail("Frida imported before consent"))
        paths.assert_not_called()

    def test_creation_sid_executable_module_and_version_gates_precede_attach(self):
        cases = [("created", 999, "process_changed"), ("sid", "S-1-5-21-other", "owner_mismatch"), ("exe", "other.exe", "target_mismatch"), ("dlls", ["other.dll"], "module_mismatch")]
        for key, value, code in cases:
            with self.subTest(key=key):
                original = self.native.target[key]
                self.native.target[key] = value
                with self.assertRaisesRegex(InitError, code):
                    worker.execute(self.args, native=self.native, loader=lambda _: self.fail("Frida imported before gate"))
                self.native.target[key] = original
        self.native.version_value = "4.1.15.57"
        with self.assertRaisesRegex(InitError, "unsupported_version"):
            worker.execute(self.args, native=self.native, loader=lambda _: self.fail("wrong version attached"))

    def test_protocol_error_detaches_without_exposing_error_detail(self):
        module, session, script, _ = self.fake_frida([({"type": "error", "description": "private-memory-"+self.native.key.hex()}, None)])
        out = io.StringIO()
        with contextlib.redirect_stdout(out), self.assertRaisesRegex(InitError, "capture_protocol"):
            self.run_worker(module)
        self.assertEqual(out.getvalue(), "")
        self.assertTrue(session.detached and script.unloaded and self.native.closed)

    def test_call_and_material_caps(self):
        values = worker.Materials()
        for i in range(1, worker.MAX_CALLS + 1):
            values.on_message(sent("call", count=i), None)
        values.on_message(sent("call", count=worker.MAX_CALLS+1), None)
        with self.assertRaisesRegex(InitError, "capture_protocol"):
            values.snapshot()
        values.clear()
        values = worker.Materials()
        for _ in range(worker.MAX_CAPTURE_BYTES // 512):
            values.on_message(sent("material", tag="at", size=512), bytes(512))
        self.assertEqual(values.total, worker.MAX_CAPTURE_BYTES)
        values.on_message(sent("material", tag="at", size=1), b"x")
        with self.assertRaisesRegex(InitError, "capture_protocol"):
            values.snapshot()
        self.assertLessEqual(values.total, worker.MAX_CAPTURE_BYTES)
        refs = [value for _, value in values.blobs]
        values.clear()
        self.assertTrue(all(not any(value) for value in refs))

    def test_nonmatching_material_never_saves_a_key(self):
        module, session, script, _ = self.fake_frida([(sent("ready"), None), (sent("material", tag="deref0", size=32), b"x"*32), (sent("call", count=1), None), (sent("done"), None)])
        with self.assertRaisesRegex(InitError, "key_not_found"):
            self.run_worker(module)
        self.assertIsNone(self.native.saved)
        self.assertTrue(session.detached and script.unloaded)

    def test_cli_prints_only_safe_error_codes(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = worker.main(["--pid", "private-argument-"+self.native.key.hex()])
        self.assertEqual(code, 2)
        self.assertEqual(json.loads(out.getvalue()), {"event": "error", "code": "invalid_arguments"})
        self.assertEqual(err.getvalue(), "")
        out = io.StringIO()
        with patch.object(worker, "execute", side_effect=RuntimeError("private-"+self.native.key.hex())), contextlib.redirect_stdout(out):
            code = worker.main(["--pid", "42001", "--created-filetime", "123456", "--dll", "private-path", "--database", "private-path", "--account-id", "wxid_test", "--consent-account", "wxid_test", "--output", "private-path"])
        self.assertEqual(code, 2)
        self.assertEqual(json.loads(out.getvalue()), {"event": "error", "code": "initialization_failed"})

    def test_timeout_upper_bound_and_short_page(self):
        self.args.timeout = 301
        with self.assertRaisesRegex(InitError, "invalid_timeout"):
            worker.execute(self.args, native=self.native)
        self.args.timeout = 10
        self.native.database.write_bytes(b"short synthetic page")
        with self.assertRaisesRegex(InitError, "database_format"):
            worker.execute(self.args, native=self.native, loader=lambda _: self.fail("short page attached"))

    def test_expired_capture_still_unloads_and_detaches(self):
        module, session, script, _ = self.fake_frida([(sent("ready"), None)])
        times = iter([0.0, 0.0, 20.0])
        with self.assertRaisesRegex(InitError, "timeout"):
            self.run_worker(module, clock=lambda: next(times, 20.0))
        self.assertTrue(session.detached and script.unloaded and self.native.closed)
        self.assertIsNone(self.native.saved)

    def test_candidate_limit_is_total_and_no_result_is_saved(self):
        module, session, script, _ = self.fake_frida()
        candidates = [("synthetic", i.to_bytes(32, "little")) for i in range(worker.MAX_CANDIDATES+1)]
        with patch.object(worker.recover, "iter_candidates", return_value=iter(candidates)), patch.object(worker.wx_crypto, "check_page1", return_value=False), self.assertRaisesRegex(InitError, "candidate_limit"):
            self.run_worker(module)
        self.assertIsNone(self.native.saved)
        self.assertTrue(session.detached and script.unloaded)

    def test_target_termination_and_cleanup_failures_are_not_successes(self):
        module, session, script, _ = self.fake_frida()
        def terminate():
            script.callback(sent("ready"), None)
            session.callback("process-terminated", None)
        script.load = terminate
        with self.assertRaisesRegex(InitError, "process_changed"):
            self.run_worker(module)
        self.assertIsNone(self.native.saved)
        module, session, script, _ = self.fake_frida()
        def detach_failure():
            raise RuntimeError("private Frida exception")
        session.detach = detach_failure
        with self.assertRaisesRegex(InitError, "cleanup_failed"):
            self.run_worker(module)
        self.assertIsNone(self.native.saved)

    def test_valid_same_file_checkpoint_page_is_accepted(self):
        payload = json.loads((Path(__file__).parent / "testdata" / "synthetic_page.json").read_text("utf-8"))
        updated = base64.b64decode(payload["checkpoint_page_base64"], validate=True)
        original = self.native.database.read_bytes()
        self.assertNotEqual(original, updated)
        self.assertEqual(original[:16], updated[:16])
        self.assertTrue(wx_crypto.check_page1(self.native.key, updated))
        def checkpoint(*args, **kwargs):
            self.native.database.write_bytes(updated)
            return bytearray(self.native.key)
        with patch.object(worker, "capture", side_effect=checkpoint), patch.object(self.native, "paths", wraps=self.native.paths) as paths:
            self.assertEqual(worker.execute(self.args, native=self.native), "initialized")
        self.assertEqual(paths.call_count, 2)
        self.assertIsNotNone(self.native.saved)

    def test_replaced_file_changed_salt_and_invalid_latest_hmac_are_rejected(self):
        original = self.native.database.read_bytes()
        for mode in ("file", "salt", "hmac"):
            with self.subTest(mode=mode):
                self.native.database.write_bytes(original)
                self.native.saved = None
                def mutate(*args, **kwargs):
                    if mode == "file":
                        replacement = self.native.database.with_name("replacement.db")
                        replacement.write_bytes(original)
                        os.replace(replacement, self.native.database)
                    else:
                        changed = bytearray(original)
                        changed[0 if mode == "salt" else 80] ^= 1
                        self.native.database.write_bytes(changed)
                    return bytearray(self.native.key)
                with patch.object(worker, "capture", side_effect=mutate), self.assertRaisesRegex(InitError, "database_changed"):
                    worker.execute(self.args, native=self.native)
                self.assertIsNone(self.native.saved)

    def test_frida_loader_removes_project_paths_and_checks_pinned_venv_origin(self):
        root = self.native.dll.parent
        prefix = root / "venv"
        site = prefix / "Lib" / "site-packages"
        base = root / "base"
        standard = base / "Lib"
        supplied = ["", str(worker.ROOT), str(Path(worker.__file__).parent), str(site), str(standard), str(base / "Lib" / "site-packages")]
        paths = {"purelib": str(site), "platlib": str(site), "stdlib": str(standard), "platstdlib": str(standard)}
        module = SimpleNamespace(__file__=str(site / "frida" / "__init__.py"), __version__=worker.FRIDA_VERSION)
        def importing(name):
            self.assertEqual(name, "frida")
            self.assertEqual(sys.path, [str(site.resolve()), str(standard.resolve())])
            return module
        with patch.object(sys, "prefix", str(prefix)), patch.object(sys, "base_prefix", str(base)), patch.object(sys, "base_exec_prefix", str(base)), patch.object(sys, "path", supplied[:]), patch.object(worker.sysconfig, "get_paths", return_value=paths), patch.object(worker.importlib.util, "find_spec", return_value=SimpleNamespace(origin=module.__file__)), patch.object(worker.importlib, "import_module", side_effect=importing):
            self.assertIs(worker.load_frida("frida"), module)
            self.assertEqual(sys.path, supplied)

    def test_frida_loader_rejects_project_shadow_and_version_mismatch(self):
        root = self.native.dll.parent
        prefix, base = root / "venv", root / "base"
        site = prefix / "Lib" / "site-packages"
        paths = {"purelib": str(site), "platlib": str(site), "stdlib": str(base / "Lib"), "platstdlib": str(base / "Lib")}
        for origin, version, code in ((str(worker.ROOT / "frida.py"), worker.FRIDA_VERSION, "frida_untrusted"), (str(site / "frida" / "__init__.py"), "17.0.0", "frida_version")):
            with self.subTest(code=code), patch.object(sys, "prefix", str(prefix)), patch.object(sys, "base_prefix", str(base)), patch.object(sys, "base_exec_prefix", str(base)), patch.object(sys, "path", [str(site), str(base / "Lib")]), patch.object(worker.sysconfig, "get_paths", return_value=paths), patch.object(worker.importlib.util, "find_spec", return_value=SimpleNamespace(origin=origin)), patch.object(worker.importlib, "import_module", return_value=SimpleNamespace(__file__=origin, __version__=version)), self.assertRaisesRegex(InitError, code):
                worker.load_frida("frida")
class OwnProcessMetadataTests(unittest.TestCase):
    @unittest.skipUnless(os.name == "nt", "Windows metadata API")
    def test_current_python_pid_only_has_current_sid_and_stable_filetime(self):
        # This is the current synthetic-test Python process, never WeChat.
        # No Frida import/attach and no enumeration of processes or accounts.
        native = Native()
        try:
            first = native.inspect(os.getpid())
            second = native.inspect(os.getpid())
            self.assertTrue(first["sid"] == first["current_sid"] and first["sid"].startswith("S-1-"), "current-process owner metadata mismatch")
            self.assertTrue(first["created"] > 0 and first["created"] == second["created"], "current-process creation time mismatch")
            self.assertTrue(Path(first["exe"]).name.casefold() == Path(__import__("sys").executable).name.casefold(), "current-process executable mismatch")
            self.assertEqual(first["dlls"], [])
        finally:
            native.close()


if __name__ == "__main__":
    unittest.main()
