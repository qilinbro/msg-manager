"""Windows gates for one explicitly selected process and one protected output.

No process enumeration, application launch, elevation, or network operations.
"""
from __future__ import annotations

import base64
import ctypes
from ctypes import wintypes
import json
import os
from pathlib import Path
import re


class InitError(Exception):
    def __init__(self, code: str):
        super().__init__(code)
        self.code = code


def canonical(path: str, *, exists: bool = True) -> Path:
    if os.name != "nt" or not os.path.isabs(path) or path.startswith(("\\\\", "//")) or "\x00" in path:
        raise InitError("invalid_path")
    result = Path(os.path.abspath(path))
    if len(result.drive) != 2 or ":" in str(result)[2:]:
        raise InitError("invalid_path")
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.GetFileAttributesW.argtypes = [wintypes.LPCWSTR]
    kernel.GetFileAttributesW.restype = wintypes.DWORD
    for item in (result, *result.parents):
        attrs = kernel.GetFileAttributesW(str(item))
        if attrs == 0xFFFFFFFF:
            if item == result and not exists and ctypes.get_last_error() in (2, 3):
                continue
            raise InitError("invalid_path")
        if attrs & 0x400:
            raise InitError("linked_path")
    kernel.GetDriveTypeW.argtypes = [wintypes.LPCWSTR]
    kernel.GetDriveTypeW.restype = wintypes.UINT
    if kernel.GetDriveTypeW(result.drive + "\\") != 3:
        raise InitError("invalid_path")
    return result


class Blob(ctypes.Structure):
    _fields_ = [("size", wintypes.DWORD), ("data", ctypes.POINTER(ctypes.c_ubyte))]


class Native:
    def __init__(self):
        if os.name != "nt":
            raise InitError("windows_required")
        self.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        self.advapi = ctypes.WinDLL("advapi32", use_last_error=True)
        self.psapi = ctypes.WinDLL("psapi", use_last_error=True)
        self._process = None
        self._pid = None
        self._signatures()

    def _signatures(self):
        k, a, p = self.kernel, self.advapi, self.psapi
        k.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
        k.OpenProcess.restype = wintypes.HANDLE
        k.CloseHandle.argtypes = [wintypes.HANDLE]
        k.CloseHandle.restype = wintypes.BOOL
        k.GetCurrentProcess.restype = wintypes.HANDLE
        k.GetProcessTimes.argtypes = [wintypes.HANDLE] + [ctypes.POINTER(wintypes.FILETIME)] * 4
        k.GetProcessTimes.restype = wintypes.BOOL
        k.GetExitCodeProcess.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.DWORD)]
        k.GetExitCodeProcess.restype = wintypes.BOOL
        k.QueryFullProcessImageNameW.argtypes = [wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD)]
        k.QueryFullProcessImageNameW.restype = wintypes.BOOL
        k.LocalFree.argtypes = [wintypes.HLOCAL]
        k.LocalFree.restype = wintypes.HLOCAL
        a.OpenProcessToken.argtypes = [wintypes.HANDLE, wintypes.DWORD, ctypes.POINTER(wintypes.HANDLE)]
        a.OpenProcessToken.restype = wintypes.BOOL
        a.GetTokenInformation.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD, ctypes.POINTER(wintypes.DWORD)]
        a.GetTokenInformation.restype = wintypes.BOOL
        a.ConvertSidToStringSidW.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.c_void_p)]
        a.ConvertSidToStringSidW.restype = wintypes.BOOL
        p.EnumProcessModulesEx.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.HMODULE), wintypes.DWORD, ctypes.POINTER(wintypes.DWORD), wintypes.DWORD]
        p.EnumProcessModulesEx.restype = wintypes.BOOL
        p.GetModuleFileNameExW.argtypes = [wintypes.HANDLE, wintypes.HMODULE, wintypes.LPWSTR, wintypes.DWORD]
        p.GetModuleFileNameExW.restype = wintypes.DWORD

    def _sid_text(self, sid) -> str:
        out = ctypes.c_void_p()
        if not self.advapi.ConvertSidToStringSidW(sid, ctypes.byref(out)):
            raise InitError("sid_unavailable")
        try:
            return ctypes.wstring_at(out)
        finally:
            self.kernel.LocalFree(out)

    def _sid(self, process) -> str:
        token = wintypes.HANDLE()
        if not self.advapi.OpenProcessToken(process, 0x0008, ctypes.byref(token)):
            raise InitError("sid_unavailable")
        try:
            size = wintypes.DWORD()
            self.advapi.GetTokenInformation(token, 1, None, 0, ctypes.byref(size))
            if not 0 < size.value <= 65536:
                raise InitError("sid_unavailable")
            buf = ctypes.create_string_buffer(size.value)
            if not self.advapi.GetTokenInformation(token, 1, buf, size.value, ctypes.byref(size)):
                raise InitError("sid_unavailable")
            sid = ctypes.cast(buf, ctypes.POINTER(ctypes.c_void_p)).contents.value
            return self._sid_text(sid)
        finally:
            self.kernel.CloseHandle(token)

    def inspect(self, pid: int) -> dict:
        # PROCESS_QUERY_INFORMATION | PROCESS_VM_READ; only this caller-supplied PID.
        if self._pid is not None and self._pid != pid:
            raise InitError("target_mismatch")
        if self._process is None:
            self._process = self.kernel.OpenProcess(0x0400 | 0x0010, False, pid)
            self._pid = pid
        process = self._process
        if not process:
            raise InitError("process_unavailable")
        try:
            exit_code = wintypes.DWORD()
            if not self.kernel.GetExitCodeProcess(process, ctypes.byref(exit_code)) or exit_code.value != 259:
                raise InitError("process_changed")
            times = [wintypes.FILETIME() for _ in range(4)]
            if not self.kernel.GetProcessTimes(process, *(ctypes.byref(t) for t in times)):
                raise InitError("process_unavailable")
            created = (times[0].dwHighDateTime << 32) | times[0].dwLowDateTime
            exe = ctypes.create_unicode_buffer(32768)
            length = wintypes.DWORD(len(exe))
            if not self.kernel.QueryFullProcessImageNameW(process, 0, exe, ctypes.byref(length)):
                raise InitError("process_unavailable")
            modules = (wintypes.HMODULE * 2048)()
            needed = wintypes.DWORD()
            if not self.psapi.EnumProcessModulesEx(process, modules, ctypes.sizeof(modules), ctypes.byref(needed), 3) or needed.value > ctypes.sizeof(modules):
                raise InitError("module_unavailable")
            dlls = []
            for module in modules[:needed.value // ctypes.sizeof(wintypes.HMODULE)]:
                name = ctypes.create_unicode_buffer(32768)
                if self.psapi.GetModuleFileNameExW(process, module, name, len(name)) and Path(name.value).name.casefold() == "weixin.dll":
                    dlls.append(name.value)
            return {"created": created, "exe": exe.value, "dlls": dlls, "sid": self._sid(process), "current_sid": self._sid(self.kernel.GetCurrentProcess())}
        finally:
            # Keep this handle until initialization finishes: holding the
            # original process object prevents its PID being recycled.
            pass

    def close(self):
        if self._process is not None:
            self.kernel.CloseHandle(self._process)
            self._process = None

    @staticmethod
    def version(path: Path) -> str:
        from third_party.flandora_wxkey.locator import _file_version
        return _file_version(path)

    def paths(self, args):
        dll = canonical(args.dll)
        database = canonical(args.database)
        if dll.name.casefold() != "weixin.dll" or not dll.is_file() or database.name.casefold() != "session.db" or not database.is_file():
            raise InitError("invalid_path")
        # Bind the one file to the selected account; no account-folder discovery.
        if database.parent.name.casefold() != "session" or database.parent.parent.name.casefold() != "db_storage":
            raise InitError("account_path_mismatch")
        account_dir = database.parent.parent.parent.name
        if account_dir != args.account_id and not re.fullmatch(re.escape(args.account_id) + r"_[A-Za-z0-9]{4}", account_dir):
            raise InitError("account_path_mismatch")
        output = canonical(args.output, exists=False)
        if os.path.lexists(output) or not output.parent.is_dir():
            raise InitError("output_exists")
        self._private_directory(output.parent)
        return dll, database, output

    def _private_directory(self, path: Path):
        # The parent must be ACL-protected and owned by this Windows user. Only
        # that SID, SYSTEM and Administrators may have allow ACEs.
        a = self.advapi
        a.GetNamedSecurityInfoW.argtypes = [wintypes.LPWSTR, ctypes.c_int, wintypes.DWORD] + [ctypes.POINTER(ctypes.c_void_p)] * 5
        a.GetNamedSecurityInfoW.restype = wintypes.DWORD
        a.GetSecurityDescriptorControl.argtypes = [ctypes.c_void_p, ctypes.POINTER(wintypes.WORD), ctypes.POINTER(wintypes.DWORD)]
        a.GetSecurityDescriptorControl.restype = wintypes.BOOL
        a.GetAce.argtypes = [ctypes.c_void_p, wintypes.DWORD, ctypes.POINTER(ctypes.c_void_p)]
        a.GetAce.restype = wintypes.BOOL
        owner, group, dacl, sacl, sd = (ctypes.c_void_p() for _ in range(5))
        status = a.GetNamedSecurityInfoW(str(path), 1, 0x1 | 0x4, ctypes.byref(owner), ctypes.byref(group), ctypes.byref(dacl), ctypes.byref(sacl), ctypes.byref(sd))
        if status:
            raise InitError("output_permissions")
        try:
            control, revision = wintypes.WORD(), wintypes.DWORD()
            if not a.GetSecurityDescriptorControl(sd, ctypes.byref(control), ctypes.byref(revision)) or not control.value & 0x1000 or not dacl.value:
                raise InitError("output_permissions")
            current_sid = self._sid(self.kernel.GetCurrentProcess())
            if self._sid_text(owner) != current_sid:
                raise InitError("output_permissions")
            # ACL header: revision/reserved/size/ace_count/reserved (8 bytes).
            header = ctypes.string_at(dacl, 8)
            count = int.from_bytes(header[4:6], "little")
            if not 0 < count <= 32:
                raise InitError("output_permissions")
            allowed = {current_sid, "S-1-5-18", "S-1-5-32-544"}
            for i in range(count):
                ace = ctypes.c_void_p()
                if not a.GetAce(dacl, i, ctypes.byref(ace)):
                    raise InitError("output_permissions")
                ace_type = ctypes.string_at(ace, 1)[0]
                if ace_type != 0 or self._sid_text(ace.value + 8) not in allowed:
                    raise InitError("output_permissions")
        finally:
            self.kernel.LocalFree(sd)

    def protect(self, secret: bytes) -> bytes:
        crypt = ctypes.WinDLL("crypt32", use_last_error=True)
        crypt.CryptProtectData.argtypes = [ctypes.POINTER(Blob), wintypes.LPCWSTR, ctypes.POINTER(Blob), ctypes.c_void_p, ctypes.c_void_p, wintypes.DWORD, ctypes.POINTER(Blob)]
        crypt.CryptProtectData.restype = wintypes.BOOL
        raw = (ctypes.c_ubyte * len(secret)).from_buffer_copy(secret)
        entropy_bytes = b"msg-manager/wechat-init/v1"
        entropy = (ctypes.c_ubyte * len(entropy_bytes)).from_buffer_copy(entropy_bytes)
        incoming, extra, out = Blob(len(raw), raw), Blob(len(entropy), entropy), Blob()
        try:
            if not crypt.CryptProtectData(ctypes.byref(incoming), "msg-manager WeChat init", ctypes.byref(extra), None, None, 0x1, ctypes.byref(out)):
                raise InitError("protect_failed")
            try:
                return ctypes.string_at(out.data, out.size)
            finally:
                self.kernel.LocalFree(out.data)
        finally:
            ctypes.memset(raw, 0, len(raw))

    def write_output(self, path: Path, payload: dict):
        canonical(str(path), exists=False)
        self._private_directory(path.parent)
        if os.path.lexists(path):
            raise InitError("output_exists")
        # Exclusive creation avoids overwriting an existing file or final link.
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_BINARY", 0), 0o600)
        try:
            with os.fdopen(fd, "wb") as target:
                target.write(json.dumps(payload, separators=(",", ":"), ensure_ascii=True).encode("ascii"))
                target.flush()
                os.fsync(target.fileno())
        except BaseException:
            # A failed new file contains DPAPI ciphertext only. Do not unlink
            # a path that may have been exchanged by another local process.
            raise InitError("output_failed") from None
