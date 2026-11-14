# Vendored subset of Flandora, commit 572447dadafbd064d622f42fa2d2013871201bb4.
# MIT; original copyright and full license are retained in LICENSE.
# Adapted here to expose offline helpers only; see SOURCE.md.

import hashlib
import hmac
import struct
import threading

PAGE = 4096
RESERVE = 80
IV_SZ = 16
KDF_ITER = 256000
SALT_MASK = 0x3A
MAC_END = PAGE - RESERVE + IV_SZ
_KEY_CACHE = {}
_KEY_CACHE_LIMIT = 16
_KEY_CACHE_LOCK = threading.Lock()

def derive_keys(passphrase: bytes, salt: bytes) -> tuple:
    """由口令与页 1 的 salt 派生 (enc_key, mac_key)。

    256000 轮 PBKDF2 一次要 190 ms 左右，而每个库的 salt 是固定的：派生结果
    按 (口令摘要, salt) 缓存在进程里，同一个库刷新多次只付一次派生成本。
    换口令或换 salt（换账号、库重建）都会自然落到另一条缓存上。
    """
    cache_key = (hashlib.sha256(passphrase).digest(), salt)
    with _KEY_CACHE_LOCK:
        hit = _KEY_CACHE.get(cache_key)
    if hit is not None:
        return hit
    enc = hashlib.pbkdf2_hmac("sha512", passphrase, salt, KDF_ITER, dklen=32)
    mac_salt = bytes(x ^ SALT_MASK for x in salt)
    mac = hashlib.pbkdf2_hmac("sha512", enc, mac_salt, 2, dklen=32)
    with _KEY_CACHE_LOCK:
        if len(_KEY_CACHE) >= _KEY_CACHE_LIMIT:
            _KEY_CACHE.clear()
        _KEY_CACHE[cache_key] = (enc, mac)
    return enc, mac


def page_mac(mac_key: bytes, page: bytes, pgno: int, skip_salt: bool) -> bytes:
    lo = 16 if skip_salt else 0
    h = hmac.new(mac_key, page[lo:MAC_END], hashlib.sha512)
    h.update(struct.pack("<I", pgno))
    return h.digest()


def page_ok(mac_key: bytes, page: bytes, pgno: int, skip_salt: bool) -> bool:
    return hmac.compare_digest(page_mac(mac_key, page, pgno, skip_salt), page[MAC_END:PAGE])


def check_page1(passphrase: bytes, page1: bytes) -> bool:
    """只凭页 1 判断口令对不对——下单库校验收敛到这一个入口。"""
    if len(page1) < PAGE:
        return False
    _, mac = derive_keys(passphrase, page1[:16])
    return page_ok(mac, page1, 1, skip_salt=True)


# --------------------------------------------------------------------------
# 页解密
# --------------------------------------------------------------------------


