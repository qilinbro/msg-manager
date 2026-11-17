"""One request per process. Credentials stay in Windows CurrentUser DPAPI.

This module does not connect or load credentials when imported. The command-line
service accepts only probe/dialogs/read over stdin; there is no send operation.
"""
from __future__ import annotations

import asyncio
import base64
import ctypes
from ctypes import wintypes
import datetime as dt
import getpass
import json
import logging
import os
from pathlib import Path
import re
import sys
import tempfile
from typing import Any

PROJECT_ROOT = Path(__file__).absolute().parents[2]
CREDENTIAL_PATH = PROJECT_ROOT / "data" / "private" / "telegram-user.dpapi"
ENTROPY = b"msg-manager/telegram-user/v1"
MAX_INPUT = 8192
MAX_CREDENTIAL = 65536
MAX_DIALOGS = 100
MAX_MESSAGES = 100
ID_PATTERN = re.compile(r"^[1-9][0-9]{0,18}$", re.ASCII)
HASH_PATTERN = re.compile(r"^[a-fA-F0-9]{32}$", re.ASCII)
SESSION_PATTERN = re.compile(r"^[A-Za-z0-9_=\-]{100,4096}$", re.ASCII)


class Failure(Exception):
    def __init__(self, code: str, retry_after: int | None = None):
        super().__init__(code)
        self.code, self.retry_after = code, retry_after


def exact_id(value: Any) -> str:
    if isinstance(value, int) and not isinstance(value, bool):
        value = str(value)
    if not isinstance(value, str) or not ID_PATTERN.fullmatch(value) or int(value) > 2**63 - 1:
        raise Failure("INVALID_INPUT")
    return value


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise Failure("INVALID_INPUT")
        result[key] = value
    return result


def decode_object(raw: bytes) -> dict:
    try:
        result = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object,
                            parse_constant=lambda _: (_ for _ in ()).throw(Failure("INVALID_INPUT")))
    except (ValueError, UnicodeError, RecursionError):
        raise Failure("INVALID_INPUT") from None
    if type(result) is not dict:
        raise Failure("INVALID_INPUT")
    return result


def parse_request(raw: bytes) -> dict:
    if len(raw) > MAX_INPUT:
        raise Failure("INVALID_INPUT")
    request = decode_object(raw)
    action = request.get("action")
    allowed = {"probe": {"action"}, "dialogs": {"action", "limit"},
               "read": {"action", "peer_id", "limit"}}
    if type(action) is not str or action not in allowed or set(request) - allowed[action]:
        raise Failure("INVALID_INPUT")
    if action == "read":
        if type(request.get("peer_id")) is not str:
            raise Failure("INVALID_INPUT")
        request["peer_id"] = exact_id(request.get("peer_id"))
    if action in ("dialogs", "read"):
        limit = request.get("limit", 100 if action == "dialogs" else 50)
        if type(limit) is not int or not 1 <= limit <= 100:
            raise Failure("INVALID_INPUT")
        request["limit"] = limit
    return request


def validate_credentials(value: Any) -> dict:
    expected = {"api_id", "api_hash", "session", "account_id", "proxy_port"}
    if type(value) is not dict or set(value) != expected:
        raise Failure("CONFIG_INVALID")
    if type(value["api_id"]) is not int or not 1 <= value["api_id"] <= 2**31 - 1:
        raise Failure("CONFIG_INVALID")
    if not isinstance(value["api_hash"], str) or not HASH_PATTERN.fullmatch(value["api_hash"]):
        raise Failure("CONFIG_INVALID")
    if not isinstance(value["session"], str) or not SESSION_PATTERN.fullmatch(value["session"]):
        raise Failure("CONFIG_INVALID")
    if type(value["account_id"]) is not str:
        raise Failure("CONFIG_INVALID")
    try:
        exact_id(value["account_id"])
    except Failure:
        raise Failure("CONFIG_INVALID") from None
    if type(value["proxy_port"]) is not int or not 0 <= value["proxy_port"] <= 65535:
        raise Failure("CONFIG_INVALID")
    return value


class _Blob(ctypes.Structure):
    _fields_ = [("cbData", wintypes.DWORD), ("pbData", ctypes.POINTER(ctypes.c_ubyte))]


def _blob(value: bytes):
    buffer = ctypes.create_string_buffer(value)
    return _Blob(len(value), ctypes.cast(buffer, ctypes.POINTER(ctypes.c_ubyte))), buffer


def dpapi(value: bytes, protect: bool) -> bytes:
    if os.name != "nt":
        raise Failure("WINDOWS_REQUIRED")
    crypt32, kernel32 = ctypes.WinDLL("crypt32", use_last_error=True), ctypes.WinDLL("kernel32", use_last_error=True)
    incoming, incoming_buffer = _blob(value)
    entropy, entropy_buffer = _blob(ENTROPY)
    outgoing = _Blob()
    kernel32.LocalFree.argtypes = [ctypes.c_void_p]
    kernel32.LocalFree.restype = ctypes.c_void_p
    function = crypt32.CryptProtectData if protect else crypt32.CryptUnprotectData
    function.argtypes = [ctypes.POINTER(_Blob), ctypes.c_void_p, ctypes.POINTER(_Blob), ctypes.c_void_p,
                         ctypes.c_void_p, wintypes.DWORD, ctypes.POINTER(_Blob)]
    function.restype = wintypes.BOOL
    if not function(ctypes.byref(incoming), None, ctypes.byref(entropy), None, None, 1, ctypes.byref(outgoing)):
        raise Failure("CONFIG_UNAVAILABLE")
    try:
        if outgoing.cbData > MAX_CREDENTIAL:
            raise Failure("CONFIG_INVALID")
        return ctypes.string_at(outgoing.pbData, outgoing.cbData)
    finally:
        if outgoing.pbData:
            ctypes.memset(outgoing.pbData, 0, outgoing.cbData)
            kernel32.LocalFree(outgoing.pbData)


def validate_local_path(path: Path, allow_missing: bool = False):
    if os.name != "nt":
        raise Failure("WINDOWS_REQUIRED")
    absolute = Path(os.path.abspath(path))
    text = str(absolute)
    if not absolute.is_absolute() or text.startswith(("\\\\", "//")) or ":" in text[2:]:
        raise Failure("CONFIG_UNSAFE")
    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel32.GetDriveTypeW.argtypes, kernel32.GetDriveTypeW.restype = [wintypes.LPCWSTR], wintypes.UINT
    if kernel32.GetDriveTypeW(absolute.anchor) != 3:
        raise Failure("CONFIG_UNSAFE")
    for item in [absolute, *absolute.parents]:
        try:
            info = item.lstat()
        except FileNotFoundError:
            if item != absolute or not allow_missing:
                raise Failure("CONFIG_MISSING") from None
            continue
        if getattr(info, "st_file_attributes", 0) & 0x400:
            raise Failure("CONFIG_UNSAFE")
    return absolute


def validate_private_acl(path: Path, directory: bool):
    """Reject another owner/identity; the credential parent must be protected."""
    if os.name != "nt":
        raise Failure("WINDOWS_REQUIRED")
    advapi = ctypes.WinDLL("advapi32", use_last_error=True)
    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    pointer = ctypes.c_void_p
    kernel32.GetCurrentProcess.restype = wintypes.HANDLE
    kernel32.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel32.LocalFree.argtypes, kernel32.LocalFree.restype = [pointer], pointer
    advapi.OpenProcessToken.argtypes, advapi.OpenProcessToken.restype = [wintypes.HANDLE, wintypes.DWORD, ctypes.POINTER(wintypes.HANDLE)], wintypes.BOOL
    advapi.GetTokenInformation.argtypes = [wintypes.HANDLE, ctypes.c_int, pointer, wintypes.DWORD, ctypes.POINTER(wintypes.DWORD)]
    advapi.GetTokenInformation.restype = wintypes.BOOL
    advapi.GetNamedSecurityInfoW.argtypes = [wintypes.LPWSTR, ctypes.c_int, wintypes.DWORD,
        ctypes.POINTER(pointer), pointer, ctypes.POINTER(pointer), pointer, ctypes.POINTER(pointer)]
    advapi.GetNamedSecurityInfoW.restype = wintypes.DWORD
    advapi.EqualSid.argtypes, advapi.EqualSid.restype = [pointer, pointer], wintypes.BOOL
    advapi.GetSecurityDescriptorControl.argtypes = [pointer, ctypes.POINTER(wintypes.WORD), ctypes.POINTER(wintypes.DWORD)]
    advapi.GetSecurityDescriptorControl.restype = wintypes.BOOL
    advapi.GetAclInformation.argtypes = [pointer, pointer, wintypes.DWORD, ctypes.c_int]
    advapi.GetAclInformation.restype = wintypes.BOOL
    advapi.GetAce.argtypes, advapi.GetAce.restype = [pointer, wintypes.DWORD, ctypes.POINTER(pointer)], wintypes.BOOL
    token, descriptor = wintypes.HANDLE(), pointer()
    try:
        if not advapi.OpenProcessToken(kernel32.GetCurrentProcess(), 8, ctypes.byref(token)):
            raise Failure("CONFIG_UNSAFE")
        length = wintypes.DWORD()
        advapi.GetTokenInformation(token, 1, None, 0, ctypes.byref(length))
        token_info = ctypes.create_string_buffer(length.value)
        if not advapi.GetTokenInformation(token, 1, token_info, length.value, ctypes.byref(length)):
            raise Failure("CONFIG_UNSAFE")
        current_sid = pointer.from_buffer(token_info).value
        owner, acl = pointer(), pointer()
        if advapi.GetNamedSecurityInfoW(str(path), 1, 5, ctypes.byref(owner), None,
                                       ctypes.byref(acl), None, ctypes.byref(descriptor)) != 0:
            raise Failure("CONFIG_UNSAFE")
        if not owner or not acl or not advapi.EqualSid(owner, current_sid):
            raise Failure("CONFIG_UNSAFE")
        control, revision = wintypes.WORD(), wintypes.DWORD()
        if not advapi.GetSecurityDescriptorControl(descriptor, ctypes.byref(control), ctypes.byref(revision)):
            raise Failure("CONFIG_UNSAFE")
        if directory and not control.value & 0x1000:
            raise Failure("CONFIG_UNSAFE")
        info = (wintypes.DWORD * 3)()
        if not advapi.GetAclInformation(acl, ctypes.byref(info), ctypes.sizeof(info), 2) or info[0] == 0:
            raise Failure("CONFIG_UNSAFE")
        full_control = False
        for index in range(info[0]):
            ace = pointer()
            if not advapi.GetAce(acl, index, ctypes.byref(ace)):
                raise Failure("CONFIG_UNSAFE")
            header = ctypes.string_at(ace, 4)
            if header[0] != 0 or int.from_bytes(header[2:], "little") < 12:
                raise Failure("CONFIG_UNSAFE")
            mask = ctypes.c_uint32.from_address(ace.value + 4).value
            if not advapi.EqualSid(ace.value + 8, current_sid):
                raise Failure("CONFIG_UNSAFE")
            full_control |= (mask & 0x1F01FF) == 0x1F01FF or bool(mask & 0x10000000)
        if not full_control:
            raise Failure("CONFIG_UNSAFE")
    finally:
        if descriptor:
            kernel32.LocalFree(descriptor)
        if token:
            kernel32.CloseHandle(token)


def load_credentials(path: Path = CREDENTIAL_PATH) -> dict:
    try:
        path = validate_local_path(path)
    except Failure as error:
        if error.code == "CONFIG_MISSING":
            raise Failure("NOT_CONFIGURED") from None
        raise
    validate_private_acl(path.parent, True)
    validate_private_acl(path, False)
    if not path.is_file() or not 1 <= path.stat().st_size <= MAX_CREDENTIAL:
        raise Failure("CONFIG_INVALID")
    try:
        with path.open("rb") as source:
            raw = source.read(MAX_CREDENTIAL + 1)
        if not 1 <= len(raw) <= MAX_CREDENTIAL:
            raise Failure("CONFIG_INVALID")
        protected = base64.b64decode(raw, validate=True)
        plain = dpapi(protected, False)
        return validate_credentials(decode_object(plain))
    except (ValueError, OSError):
        raise Failure("CONFIG_UNAVAILABLE") from None


def save_credentials(value: dict, replace: bool = False, path: Path = CREDENTIAL_PATH):
    validate_credentials(value)
    path = validate_local_path(path, True)
    validate_private_acl(path.parent, True)
    if path.exists():
        if not replace:
            raise Failure("CONFIG_EXISTS")
        validate_private_acl(path, False)
    encrypted = base64.b64encode(dpapi(json.dumps(value, separators=(",", ":")).encode("utf-8"), True))
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(prefix="telegram-", suffix=".dpapi.tmp", dir=path.parent, delete=False) as output:
            temporary = Path(output.name)
            validate_private_acl(temporary, False)
            output.write(encrypted)
            output.flush()
            os.fsync(output.fileno())
        if not replace and path.exists():
            raise Failure("CONFIG_EXISTS")
        os.replace(temporary, path)
        temporary = None
        validate_private_acl(path, False)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def label(entity: Any) -> str:
    text = " ".join(part for part in (getattr(entity, "first_name", None), getattr(entity, "last_name", None)) if part)
    text = text or getattr(entity, "username", None) or str(getattr(entity, "id", ""))
    return "".join(ch for ch in text if ch.isprintable()).strip()[:128]


def error_result(error: BaseException) -> dict:
    code, retry = "UNAVAILABLE", None
    if isinstance(error, Failure):
        code, retry = error.code, error.retry_after
    else:
        kind = type(error).__name__
        if kind in ("FloodWaitError", "FloodPremiumWaitError", "SlowModeWaitError"):
            code = "FLOOD_WAIT"
            retry = max(1, min(int(getattr(error, "seconds", 60)), 7 * 86400))
        elif kind in ("AuthKeyUnregisteredError", "SessionRevokedError", "SessionExpiredError", "UnauthorizedError", "UserDeactivatedError", "UserDeactivatedBanError"):
            code = "AUTH_REQUIRED"
        elif kind in ("ApiIdInvalidError", "ApiIdPublishedFloodError"):
            code = "API_INVALID"
        elif kind in ("PhoneCodeExpiredError", "PhoneCodeInvalidError", "PhoneNumberInvalidError", "PasswordHashInvalidError"):
            code = "LOGIN_INVALID"
        elif isinstance(error, (TimeoutError, asyncio.TimeoutError, ConnectionError, OSError)):
            code = "UNREACHABLE"
        elif isinstance(error, (ImportError, ModuleNotFoundError)):
            code = "DEPENDENCY_MISSING"
    result = {"ok": False, "error": {"code": code}}
    if retry is not None:
        result["error"]["retry_after"] = retry
    return result


def new_client(credentials: dict):
    from telethon import TelegramClient
    from telethon.sessions import StringSession
    proxy = None
    if credentials["proxy_port"]:
        proxy = {"proxy_type": "socks5", "addr": "127.0.0.1", "port": credentials["proxy_port"], "rdns": True}
    return TelegramClient(StringSession(credentials["session"]), credentials["api_id"], credentials["api_hash"],
                          timeout=10, request_retries=0, connection_retries=0, auto_reconnect=False,
                          raise_last_call_error=True,
                          flood_sleep_threshold=0, receive_updates=False, proxy=proxy,
                          device_model="Social Intelligence OS", app_version="0.1.0")


def is_private_user(entity, types_module) -> bool:
    return isinstance(entity, types_module.User) and not any(getattr(entity, attr, False) for attr in ("bot", "is_self", "deleted"))


async def private_dialogs(client, types_module, limit: int = 100):
    items, peers, limited, count = [], {}, False, 0
    async for dialog in client.iter_dialogs(limit=limit + 1):
        count += 1
        if count > limit:
            limited = True
            break
        entity = dialog.entity
        if not is_private_user(entity, types_module):
            continue
        peer_id = exact_id(entity.id)
        items.append({"peer_id": peer_id, "name": label(entity)})
        peers[peer_id] = entity
    return items, peers, limited


def normalize_message(message, self_id: str, peer_id: str) -> dict | None:
    body = getattr(message, "raw_text", None)
    if not isinstance(body, str) or not body or "\x00" in body or len(body.encode("utf-8")) > 16000:
        return None
    if getattr(message, "action", None) is not None:
        return None
    try:
        message_id = exact_id(message.id)
        sender = exact_id(message.sender_id)
    except Failure:
        return None
    if sender not in (self_id, peer_id):
        return None
    instant = getattr(message, "date", None)
    if not isinstance(instant, dt.datetime) or instant.tzinfo is None:
        return None
    instant = instant.astimezone(dt.timezone.utc)
    if instant.timestamp() < 0 or instant > dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=1):
        return None
    return {"external_id": f"telegram:user:{peer_id}:{message_id}", "body": body,
            "direction": "outgoing" if sender == self_id else "incoming",
            "occurred_at": instant.isoformat().replace("+00:00", "Z")}


async def perform(request: dict, credentials: dict, client=None, types_module=None) -> dict:
    if types_module is None:
        from telethon.tl import types as types_module
    client = client or new_client(credentials)
    try:
        await client.connect()
        me = await client.get_me()
        if me is None:
            raise Failure("AUTH_REQUIRED")
        if getattr(me, "bot", False) or exact_id(me.id) != credentials["account_id"]:
            raise Failure("IDENTITY_MISMATCH")
        common = {"account_id": credentials["account_id"], "name": label(me), "read_only": True, "sending_enabled": False}
        if request["action"] == "probe":
            return common
        items, peers, limited = await private_dialogs(client, types_module, request.get("limit", 100) if request["action"] == "dialogs" else 100)
        if request["action"] == "dialogs":
            return {**common, "items": items, "limited": limited}
        peer = peers.get(request["peer_id"])
        if peer is None:
            raise Failure("PEER_NOT_AVAILABLE")
        messages, skipped, examined = [], 0, 0
        async for message in client.iter_messages(peer, limit=request["limit"] + 1):
            examined += 1
            if examined > request["limit"]:
                break
            normalized = normalize_message(message, credentials["account_id"], request["peer_id"])
            if normalized is None:
                skipped += 1
            else:
                messages.append(normalized)
        messages.reverse()
        return {**common, "peer_id": request["peer_id"], "peer_name": label(peer), "messages": messages,
                "skipped": skipped, "limited": examined > request["limit"]}
    finally:
        await asyncio.wait_for(client.disconnect(), timeout=5)


async def login_request(operation, migration_errors: tuple):
    """Telethon has already switched DC before surfacing these exact errors.

    A migration explicitly rejects the original request. Retry it once on the
    selected DC; never retry ambiguous network failures, flood waits or codes.
    This exception is confined to the interactive login path.
    """
    try:
        return await operation()
    except migration_errors:
        return await operation()


async def initialize(replace: bool = False):
    if os.name != "nt":
        raise Failure("WINDOWS_REQUIRED")
    if not sys.stdin.isatty() or not sys.stdout.isatty():
        raise Failure("INVALID_INPUT")
    validate_local_path(CREDENTIAL_PATH, True)
    validate_private_acl(CREDENTIAL_PATH.parent, True)
    if CREDENTIAL_PATH.exists() and not replace:
        raise Failure("CONFIG_EXISTS")
    api_text = input("API ID (my.telegram.org): ").strip()
    if not re.fullmatch(r"[0-9]{1,10}", api_text, re.ASCII):
        raise Failure("API_INVALID")
    api_id = int(api_text)
    api_hash = getpass.getpass("API Hash (hidden, local only): ").strip()
    if not 1 <= api_id <= 2**31 - 1 or not HASH_PATTERN.fullmatch(api_hash):
        raise Failure("API_INVALID")
    port_text = input("Local SOCKS5 proxy port (blank = direct): ").strip()
    if port_text and not re.fullmatch(r"[0-9]{1,5}", port_text, re.ASCII):
        raise Failure("INVALID_INPUT")
    proxy_port = int(port_text) if port_text else 0
    if not 0 <= proxy_port <= 65535:
        raise Failure("INVALID_INPUT")
    from telethon import TelegramClient
    from telethon.errors import SessionPasswordNeededError, PhoneMigrateError, NetworkMigrateError, UserMigrateError
    from telethon.sessions import StringSession
    proxy = {"proxy_type": "socks5", "addr": "127.0.0.1", "port": proxy_port, "rdns": True} if proxy_port else None
    client = TelegramClient(StringSession(), api_id, api_hash, timeout=10, request_retries=0, connection_retries=0,
                            auto_reconnect=False, raise_last_call_error=True,
                            flood_sleep_threshold=0, receive_updates=False, proxy=proxy,
                            device_model="Social Intelligence OS", app_version="0.1.0")
    authenticated, retained = False, False
    migrations = (PhoneMigrateError, NetworkMigrateError, UserMigrateError)
    try:
        await client.connect()
        phone = getpass.getpass("Own phone number including country code (hidden): ").strip()
        if not re.fullmatch(r"\+[1-9][0-9]{5,14}", phone, re.ASCII):
            raise Failure("LOGIN_INVALID")
        sent = await login_request(lambda: client.send_code_request(phone), migrations)
        code = getpass.getpass("Telegram login code (hidden): ").strip()
        try:
            await login_request(lambda: client.sign_in(phone=phone, code=code, phone_code_hash=sent.phone_code_hash), migrations)
        except SessionPasswordNeededError:
            password = getpass.getpass("Telegram two-step password (hidden): ")
            await login_request(lambda: client.sign_in(password=password), migrations)
        authenticated = True
        me = await client.get_me()
        if me is None or getattr(me, "bot", False):
            raise Failure("IDENTITY_MISMATCH")
        account_id = exact_id(me.id)
        print(f"Verify this is your own account: {label(me)} (account ID: {account_id}). No chats have been imported or sent.")
        answer = input("Save this session for the local read-only connector? Type SAVE: ").strip()
        if answer != "SAVE":
            raise Failure("CANCELLED")
        save_credentials({"api_id": api_id, "api_hash": api_hash, "session": client.session.save(),
                          "account_id": account_id, "proxy_port": proxy_port}, replace)
        retained = True
        print("Encrypted Telegram configuration saved for this Windows user. Return to the workbench and verify the account.")
    finally:
        # A declined/failed save must not deliberately leave this brand-new
        # login active. Failure to reach Telegram during cleanup is not success;
        # the operator can inspect Telegram Settings > Devices afterwards.
        if authenticated and not retained:
            try:
                if await asyncio.wait_for(client.log_out(), timeout=10) is not True:
                    raise Failure("AUTH_REQUIRED")
            except Exception:
                print("The unsaved login could not be revoked. Review Telegram Settings > Devices.", file=sys.stderr)
        await asyncio.wait_for(client.disconnect(), timeout=5)


def main() -> int:
    logging.disable(logging.CRITICAL)
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    if len(sys.argv) > 1:
        if sys.argv[1:] not in (["initialize"], ["initialize", "--replace"]):
            print("Invalid helper arguments.", file=sys.stderr)
            return 2
        try:
            asyncio.run(initialize("--replace" in sys.argv))
            return 0
        except (Exception, KeyboardInterrupt) as error:
            print("Telegram initialization did not complete: " + error_result(error)["error"]["code"], file=sys.stderr)
            return 1
    try:
        request = parse_request(sys.stdin.buffer.read(MAX_INPUT + 1))
        credentials = load_credentials()
        data = asyncio.run(asyncio.wait_for(perform(request, credentials), timeout=30))
        result = {"ok": True, "data": data}
    except (Exception, KeyboardInterrupt) as error:
        result = error_result(error)
    sys.stdout.write(json.dumps(result, ensure_ascii=False, separators=(",", ":")) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
