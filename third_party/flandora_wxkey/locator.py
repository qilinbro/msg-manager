# Vendored subset of Flandora, commit 572447dadafbd064d622f42fa2d2013871201bb4.
# MIT; original copyright and full license are retained in LICENSE.
# Adapted here to expose offline helpers only; see SOURCE.md.

from __future__ import annotations
import ctypes
import hashlib
import struct
from ctypes import wintypes
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

DIR_EXCEPTION = 3
SIG_MOV_R8D = b"\x41\xb8\x00\x10\x00\x00\x41\xb9"
SIG_CALL = 0xE8
SIG_LEN = 17

class LocatorError(Exception):
    """定位失败。"""


class UnsupportedBuild(LocatorError):
    """当前微信版本的结构不符合已知调用形态，无法定位。"""


# PE 首选基址的缺省值（4.1.15.56 上是 0x180000000）；解析出真实值后以 PE 为准，
# 运行期挂钩再用实际加载基址。
DEFAULT_IMAGE_BASE = 0x180000000


@dataclass(frozen=True)
class HookPoint:
    """一个可挂钩的位置。"""

    rva: int
    size: int
    dll_path: str
    dll_sha256: str
    file_version: str
    image_base: int = DEFAULT_IMAGE_BASE

    @property
    def va(self) -> int:
        """按这张 DLL 的首选基址算出的映像内地址；运行期还要再加上实际加载基址差。"""
        return self.image_base + self.rva

    def describe(self) -> str:
        ver = self.file_version or "未知版本"
        return (
            f"微信 {ver}，口令设置函数 RVA {self.rva:#x}"
            f"（{self.size} 字节），Weixin.dll {self.dll_sha256[:16]}…"
        )


def _u16(buf: bytes, off: int) -> int:
    return struct.unpack_from("<H", buf, off)[0]


def _u32(buf: bytes, off: int) -> int:
    return struct.unpack_from("<I", buf, off)[0]


class PeImage:
    """只解析定位需要的那部分 PE 结构：段表与 .pdata 的函数边界。"""

    def __init__(self, path: Path) -> None:
        self.path = Path(path)
        try:
            self.data = self.path.read_bytes()
        except OSError as exc:
            raise LocatorError(f"读不了 {self.path}：{exc}") from exc
        self._parse_headers()
        self._parse_functions()

    def _parse_headers(self) -> None:
        data = self.data
        if data[:2] != b"MZ":
            raise LocatorError(f"{self.path} 不是 PE 文件")
        lfanew = _u32(data, 0x3C)
        if data[lfanew : lfanew + 4] != b"PE\0\0":
            raise LocatorError(f"{self.path} 的 PE 签名不对")
        n_sec = _u16(data, lfanew + 6)
        opt_size = _u16(data, lfanew + 20)
        opt = lfanew + 24
        magic = _u16(data, opt)
        if magic == 0x20B:
            dd_off = opt + 112
            preferred = struct.unpack_from("<Q", data, opt + 24)[0]
        elif magic == 0x10B:
            dd_off = opt + 96
            preferred = _u32(data, opt + 28)
        else:
            raise LocatorError(f"未知的可选头 magic {magic:#x}")
        self.image_base = preferred
        self.sections: dict[str, tuple[int, int, int, int]] = {}
        sec_off = opt + opt_size
        for i in range(n_sec):
            o = sec_off + i * 40
            name = data[o : o + 8].rstrip(b"\0").decode("latin1")
            vsize = _u32(data, o + 8)
            vaddr = _u32(data, o + 12)
            rawsize = _u32(data, o + 16)
            rawptr = _u32(data, o + 20)
            self.sections[name] = (vaddr, vsize, rawptr, rawsize)

        exc_rva = _u32(data, dd_off + DIR_EXCEPTION * 8)
        exc_size = _u32(data, dd_off + DIR_EXCEPTION * 8 + 4)
        self._exc = (exc_rva, exc_size)

    def section_of(self, rva: int) -> Optional[str]:
        for name, (va, vsize, _rp, _rs) in self.sections.items():
            if va <= rva < va + max(vsize, 1):
                return name
        return None

    def _parse_functions(self) -> None:
        exc_rva, exc_size = self._exc
        if not exc_rva or not exc_size:
            raise UnsupportedBuild("Weixin.dll 没有异常目录（.pdata），无法取得函数边界")
        raw = self._rva_to_file(exc_rva)
        if raw is None:
            raise UnsupportedBuild(".pdata 的地址换不到文件偏移")
        funcs = []
        for i in range(exc_size // 12):
            begin, end, _unwind = struct.unpack_from("<III", self.data, raw + i * 12)
            if begin and end > begin:
                funcs.append((begin, end))
        if not funcs:
            raise UnsupportedBuild(".pdata 里没有有效的函数条目")
        funcs.sort()
        self.funcs = funcs
        self._starts = [b for b, _ in funcs]

    def _rva_to_file(self, rva: int) -> Optional[int]:
        for _name, (va, vsize, rawptr, rawsize) in self.sections.items():
            if va <= rva < va + max(vsize, rawsize):
                return rawptr + (rva - va)
        return None

    def func_of(self, rva: int) -> Optional[tuple[int, int]]:
        lo, hi = 0, len(self.funcs) - 1
        while lo <= hi:
            mid = (lo + hi) // 2
            begin, end = self.funcs[mid]
            if rva < begin:
                hi = mid - 1
            elif rva >= end:
                lo = mid + 1
            else:
                return (begin, end)
        return None

    def text(self) -> tuple[int, bytes]:
        if ".text" not in self.sections:
            raise UnsupportedBuild("Weixin.dll 没有 .text 段")
        va, _vsize, rawptr, rawsize = self.sections[".text"]
        return va, self.data[rawptr : rawptr + rawsize]


def _file_version(path: Path) -> str:
    """读 PE 的版本资源；读不到就返回空串。"""
    size = ctypes.windll.version.GetFileVersionInfoSizeW(str(path), None)
    if not size:
        return ""
    buf = ctypes.create_string_buffer(size)
    if not ctypes.windll.version.GetFileVersionInfoW(str(path), 0, size, buf):
        return ""
    ptr = ctypes.c_void_p()
    length = wintypes.UINT()
    if not ctypes.windll.version.VerQueryValueW(buf, "\\", ctypes.byref(ptr), ctypes.byref(length)):
        return ""
    if not length.value:
        return ""

    class FixedFileInfo(ctypes.Structure):
        _fields_ = [
            ("dwSignature", wintypes.DWORD),
            ("dwStrucVersion", wintypes.DWORD),
            ("dwFileVersionMS", wintypes.DWORD),
            ("dwFileVersionLS", wintypes.DWORD),
            ("dwProductVersionMS", wintypes.DWORD),
            ("dwProductVersionLS", wintypes.DWORD),
            ("dwFileFlagsMask", wintypes.DWORD),
            ("dwFileFlags", wintypes.DWORD),
            ("dwFileOS", wintypes.DWORD),
            ("dwFileType", wintypes.DWORD),
            ("dwFileSubtype", wintypes.DWORD),
            ("dwFileDateMS", wintypes.DWORD),
            ("dwFileDateLS", wintypes.DWORD),
        ]

    info = ctypes.cast(ptr, ctypes.POINTER(FixedFileInfo)).contents
    ms, ls = info.dwFileVersionMS, info.dwFileVersionLS
    return f"{ms >> 16}.{ms & 0xFFFF}.{ls >> 16}.{ls & 0xFFFF}"


def scan_key_setter(image: PeImage) -> tuple[int, list[int]]:
    """按调用形态扫 .text，返回 (目标函数起始 RVA, 所有调用点 RVA)。"""
    text_va, text = image.text()
    targets: dict[int, list[int]] = {}
    sites: list[int] = []
    pos = 0
    while True:
        i = text.find(SIG_MOV_R8D, pos)
        if i < 0:
            break
        pos = i + 1
        if i + SIG_LEN > len(text):
            break
        if text[i + 12] != SIG_CALL:
            continue
        disp = struct.unpack_from("<i", text, i + 13)[0]
        site = text_va + i
        callee = site + SIG_LEN + disp
        fn = image.func_of(callee)
        if fn is None:
            continue
        sites.append(site)
        targets.setdefault(fn[0], []).append(site)

    if not targets:
        raise UnsupportedBuild(
            "在 Weixin.dll 里没有找到「传页大小与兼容级别」的调用形态，当前微信版本可能改了实现"
        )
    if len(targets) > 1:
        detail = "，".join(
            f"{rva:#x}（{len(s)} 处调用）"
            for rva, s in sorted(targets.items(), key=lambda x: -len(x[1]))
        )
        raise UnsupportedBuild(f"调用形态指向 {len(targets)} 个不同函数，无法唯一确定：{detail}")
    target_rva = next(iter(targets))
    return target_rva, sites


