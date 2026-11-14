# Flandora offline helper subset

- Upstream: https://github.com/OMSociety/Flandora
- Pinned commit: `572447dadafbd064d622f42fa2d2013871201bb4`
- License: MIT, reproduced in LICENSE with original copyright notices.
- Imported portions: `flandora/wxkey/locator.py` (PE parsing, file version, unique key-setter locator only), `flandora/wxkey/recover.py` (in-memory candidate iterator only), and `flandora/wx_crypto.py` (key derivation and page-1 HMAC only).
- Adaptations: standalone imports; removed account/process enumeration, registry and directory discovery, capture/Frida code, CLIs and plaintext-printing functions, capture-file readers/writers, database decryption/WAL/export routines. Candidate lengths default to 32 bytes. Derived-key cache is capped at 16 entries and cleared by the worker.
- Our bounded single-PID worker lives in `scripts/wechat_init/` and is original product code. Frida is a separate optional runtime dependency and is not vendored here.
- No upstream KeyTool or binary was executed. The adapted offline helpers run against public synthetic fixtures in our tests; no real WeChat process or account was used. These tests do not establish real-account compatibility.
