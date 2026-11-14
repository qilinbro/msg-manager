"""One-shot, explicit-consent initialization for one current-user Weixin PID.

This module never discovers processes/accounts, restarts WeChat, elevates, sends
messages, or writes captured memory. Importing it does not import Frida.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import importlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import sysconfig
import threading
import time

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from scripts.wechat_init.native import InitError, Native, canonical
from third_party.flandora_wxkey import locator, recover, wx_crypto

CLIENT_VERSION = "4.1.15.56"
MAX_CALLS = 12
MAX_CAPTURE_BYTES = 64 * 1024
MAX_BLOBS = 160
MAX_CANDIDATES = 256
MAX_TIMEOUT = 300
FRIDA_VERSION = "17.26.3"


def _inside(path: Path, roots) -> bool:
    return any(path == root or root in path.parents for root in roots)


def load_frida(name: str):
    """Import only the pinned Frida from this isolated virtual environment.

    ROOT is needed for our offline helpers, but must not provide frida.py or
    any dependency while the third-party package is being imported.
    """
    if name != "frida" or sys.prefix == sys.base_prefix:
        raise InitError("frida_environment")
    paths = sysconfig.get_paths()
    sites = {Path(paths[key]).resolve() for key in ("purelib", "platlib")}
    prefix = Path(sys.prefix).resolve()
    if not sites or any(not _inside(site, [prefix]) for site in sites):
        raise InitError("frida_environment")
    standard = {Path(paths[key]).resolve() for key in ("stdlib", "platstdlib")}
    standard.update({Path(sys.base_prefix).resolve() / "DLLs", Path(sys.base_exec_prefix).resolve() / "DLLs"})
    archives = {Path(sys.base_prefix).resolve() / f"python{sys.version_info.major}{sys.version_info.minor}.zip"}
    original = sys.path[:]
    try:
        approved = []
        for entry in original:
            if not entry:
                continue
            path = Path(entry).resolve()
            if path in {ROOT, Path(__file__).resolve().parent}:
                continue
            in_site = _inside(path, sites)
            package_dir = any(part.casefold() in {"site-packages", "dist-packages"} for part in path.parts)
            if in_site or (not package_dir and (_inside(path, standard) or path in archives)):
                approved.append(str(path))
        sys.path[:] = approved
        spec = importlib.util.find_spec("frida")
        if spec is None or not spec.origin or not _inside(Path(spec.origin).resolve(), sites):
            raise InitError("frida_untrusted")
        module = importlib.import_module("frida")
        origin = getattr(module, "__file__", None)
        if not origin or not _inside(Path(origin).resolve(), sites):
            raise InitError("frida_untrusted")
        if getattr(module, "__version__", None) != FRIDA_VERSION:
            raise InitError("frida_version")
        return module
    except InitError:
        raise
    except Exception:
        raise InitError("frida_unavailable") from None
    finally:
        sys.path[:] = original

# The target address comes only from the pinned offline PE locator after target
# and module verification. The caller cannot supply an RVA or another PID.
SCRIPT_TEMPLATE = r"""
'use strict';
const pidExpected = PID_VALUE;
if (Process.id !== pidExpected) throw new Error('target_mismatch');
const targetModule = Process.getModuleByName('Weixin.dll');
const target = targetModule.base.add(RVA_VALUE);
let calls = 0;
let bytes = 0;
let stopped = false;
const capCalls = CALL_CAP;
const capBytes = BYTE_CAP;
let hook = null;
function stop() {
  stopped = true;
  if (hook !== null) hook.detach();
  send({event:'done'});
}
function grab(tag, address, size) {
  if (stopped || bytes + size > capBytes) return;
  try {
    if (address.isNull()) return;
    const payload = address.readByteArray(size);
    if (payload === null) return;
    bytes += size;
    send({event:'material',tag:tag,size:size}, payload);
  } catch (_) {}
}
hook = Interceptor.attach(target, {
  onEnter(args) {
    if (stopped) return;
    calls++;
    if (calls > capCalls) { stop(); return; }
    const key = args[1];
    if (!key.isNull()) {
      // At most 8 explicit pointer slots and two small local spans per call.
      for (let i=0;i<8;i++) {
        try { grab('deref'+i,key.add(i*Process.pointerSize).readPointer(),512); } catch (_) {}
      }
      grab('at',key,512);
      grab('before',key.sub(64),64);
    }
    send({event:'call',count:calls});
    if (calls >= capCalls || bytes >= capBytes) stop();
  }
});
send({event:'ready'});
"""


class Materials:
    """Bound Frida input again on the Python side; never log material values."""
    def __init__(self):
        self.lock = threading.Lock()
        self.event = threading.Event()
        self.blobs: list[tuple[str, bytearray]] = []
        self.total = 0
        self.calls = 0
        self.done = False
        self.ready = False
        self.failed = False
        self.error_code = "capture_protocol"

    def detached(self, *args):
        with self.lock:
            self.failed = True
            self.error_code = "process_changed"
            self.event.set()

    def on_message(self, message, data):
        with self.lock:
            if self.done or self.failed:
                return
            if not isinstance(message, dict) or message.get("type") != "send":
                self.failed = True
                self.event.set()
                return
            payload = message.get("payload")
            if not isinstance(payload, dict):
                self.failed = True
            elif payload.get("event") == "material":
                tag = payload.get("tag", "")
                if not isinstance(data, bytes) or not re.fullmatch(r"(?:deref[0-7]|at|before)", str(tag)) or payload.get("size") != len(data) or not 0 < len(data) <= 512 or self.total + len(data) > MAX_CAPTURE_BYTES or len(self.blobs) >= MAX_BLOBS:
                    self.failed = True
                else:
                    self.blobs.append((tag, bytearray(data)))
                    self.total += len(data)
            elif payload.get("event") == "call":
                count = payload.get("count")
                if type(count) is not int or count != self.calls + 1 or count > MAX_CALLS:
                    self.failed = True
                else:
                    self.calls = count
            elif payload.get("event") == "done":
                self.done = True
            elif payload.get("event") == "ready":
                self.ready = True
            else:
                self.failed = True
            self.event.set()

    def snapshot(self):
        with self.lock:
            if self.failed:
                raise InitError(self.error_code)
            return [(tag, bytes(value)) for tag, value in self.blobs], self.done, self.ready

    def clear(self):
        with self.lock:
            for _, value in self.blobs:
                value[:] = b"\x00" * len(value)
            self.blobs.clear()
            self.total = 0


def validate_arguments(args):
    if not re.fullmatch(r"[A-Za-z][A-Za-z0-9_-]{1,127}", args.account_id) or args.consent_account != args.account_id:
        raise InitError("consent_required")
    if type(args.pid) is not int or not 0 < args.pid <= 0xFFFFFFFF or type(args.created_filetime) is not int or args.created_filetime <= 0:
        raise InitError("invalid_target")
    if type(args.timeout) is not int or not 1 <= args.timeout <= MAX_TIMEOUT:
        raise InitError("invalid_timeout")


def verify_target(args, native, expected_dll: Path):
    target = native.inspect(args.pid)
    if target["created"] != args.created_filetime:
        raise InitError("process_changed")
    if not target["sid"] or target["sid"] != target["current_sid"]:
        raise InitError("owner_mismatch")
    exe = native.canonical(target["exe"]) if hasattr(native, "canonical") else canonical(target["exe"])
    if exe.name.casefold() != "weixin.exe":
        raise InitError("target_mismatch")
    modules = [native.canonical(p) if hasattr(native, "canonical") else canonical(p) for p in target["dlls"]]
    if len(modules) != 1 or str(modules[0]).casefold() != str(expected_dll).casefold():
        raise InitError("module_mismatch")
    if native.version(exe) != CLIENT_VERSION or native.version(expected_dll) != CLIENT_VERSION:
        raise InitError("unsupported_version")


def read_snapshot(database: Path):
    with database.open("rb") as source:
        stat = os.fstat(source.fileno())
        page = source.read(wx_crypto.PAGE)
    if len(page) != wx_crypto.PAGE or page.startswith(b"SQLite format 3\x00"):
        raise InitError("database_format")
    return page, (stat.st_dev, stat.st_ino)


def read_page(database: Path) -> bytes:
    return read_snapshot(database)[0]


def capture(args, native, dll: Path, page: bytes, deadline: float, *, loader, clock, notify):
    # Offline, explicit-file parser only: no find_weixin_dll/resolve/discovery.
    image = locator.PeImage(dll)
    rva, _ = locator.scan_key_setter(image)
    digest = hashlib.sha256(image.data).digest()
    del image
    verify_target(args, native, dll)
    frida = loader("frida")
    material = Materials()
    session = script = None
    cancellable = frida.Cancellable()
    timer = threading.Timer(max(0.01, deadline - clock()), cancellable.cancel)
    timer.daemon = True
    timer.start()
    try:
        with cancellable:
            session = frida.attach(args.pid)
            session.on("detached", material.detached)
            # The Windows process handle remains open in Native: a terminated
            # process cannot have its PID reused under this validated handle.
            verify_target(args, native, dll)
            if hashlib.sha256(dll.read_bytes()).digest() != digest:
                raise InitError("module_changed")
            source = SCRIPT_TEMPLATE.replace("PID_VALUE", str(args.pid)).replace("RVA_VALUE", str(rva)).replace("CALL_CAP", str(MAX_CALLS)).replace("BYTE_CAP", str(MAX_CAPTURE_BYTES))
            script = session.create_script(source)
            script.on("message", material.on_message)
            script.load()
            checked: set[bytes] = set()
            announced = False
            while clock() < deadline:
                material.event.wait(min(0.2, max(0, deadline - clock())))
                material.event.clear()
                blobs, done, ready = material.snapshot()
                if ready and not announced:
                    notify("ready")
                    notify("waiting")
                    announced = True
                for _, candidate in recover.iter_candidates(blobs, lengths=(32,)):
                    if len(candidate) != 32:
                        continue
                    fingerprint = hashlib.sha256(candidate).digest()
                    if fingerprint in checked:
                        continue
                    if len(checked) >= MAX_CANDIDATES:
                        raise InitError("candidate_limit")
                    if clock() >= deadline:
                        raise InitError("timeout")
                    checked.add(fingerprint)
                    if wx_crypto.check_page1(candidate, page):
                        verify_target(args, native, dll)
                        return bytearray(candidate)
                del blobs
                if done:
                    raise InitError("key_not_found")
            raise InitError("timeout")
    except InitError:
        raise
    except Exception:
        raise InitError("capture_failed") from None
    finally:
        timer.cancel()
        cleanup_failed = False
        # Cleanup is outside the cancelled context so unload/detach can run.
        if script is not None:
            try:
                script.unload()
            except Exception:
                pass
        if session is not None:
            try:
                session.detach()
            except Exception:
                cleanup_failed = True
        material.clear()
        with wx_crypto._KEY_CACHE_LOCK:
            wx_crypto._KEY_CACHE.clear()
        if cleanup_failed:
            raise InitError("cleanup_failed") from None


def emit(event: str, code: str | None = None):
    payload = {"event": event}
    if code is not None:
        payload["code"] = code
    print(json.dumps(payload), flush=True)


def execute(args, *, native=None, loader=load_frida, clock=time.monotonic, notify=emit):
    validate_arguments(args)
    deadline = clock() + args.timeout
    target = native or Native()
    secret = None
    try:
        dll, database, output = target.paths(args)
        verify_target(args, target, dll)
        page, identity = read_snapshot(database)
        secret = capture(args, target, dll, page, deadline, loader=loader, clock=clock, notify=notify)
        # Re-login may checkpoint normal changes into the first page. Bind its
        # file identity and salt, then verify the latest page with the key.
        checked_dll, checked_database, checked_output = target.paths(args)
        if any(str(before).casefold() != str(after).casefold() for before, after in ((dll, checked_dll), (database, checked_database), (output, checked_output))):
            raise InitError("database_changed")
        latest, latest_identity = read_snapshot(checked_database)
        if clock() >= deadline or latest_identity != identity or latest[:16] != page[:16] or not wx_crypto.check_page1(bytes(secret), latest):
            raise InitError("database_changed")
        protected = target.protect(bytes(secret))
        target.write_output(output, {"format": 1, "account_id": args.account_id, "client_version": CLIENT_VERSION, "protected_key": base64.b64encode(protected).decode("ascii")})
        return "initialized"
    finally:
        if secret is not None:
            secret[:] = b"\x00" * len(secret)
        target.close()
        with wx_crypto._KEY_CACHE_LOCK:
            wx_crypto._KEY_CACHE.clear()


class SafeParser(argparse.ArgumentParser):
    def error(self, message):
        raise InitError("invalid_arguments")


def main(argv=None) -> int:
    try:
        parser = SafeParser(description="Initialize one explicitly selected current-user WeChat account")
        parser.add_argument("--pid", required=True, type=int)
        parser.add_argument("--created-filetime", required=True, type=int)
        parser.add_argument("--dll", required=True)
        parser.add_argument("--database", required=True)
        parser.add_argument("--account-id", required=True)
        parser.add_argument("--consent-account", required=True)
        parser.add_argument("--output", required=True)
        parser.add_argument("--timeout", default=180, type=int)
        result = execute(parser.parse_args(argv))
        emit("saved", result)
        return 0
    except InitError as exc:
        emit("error", exc.code)
        return 2
    except (KeyboardInterrupt, SystemExit):
        emit("error", "cancelled")
        return 2
    except Exception:
        emit("error", "initialization_failed")
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
