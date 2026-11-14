# Vendored subset of Flandora, commit 572447dadafbd064d622f42fa2d2013871201bb4.
# MIT; original copyright and full license are retained in LICENSE.
# Adapted here to expose offline helpers only; see SOURCE.md.

from __future__ import annotations
from typing import Iterator, Sequence

LEN_CANDIDATES = (32,)
ALIGN = 4

def iter_candidates(
    blobs: Sequence[tuple[str, bytes]], lengths: Sequence[int] = LEN_CANDIDATES
) -> Iterator[tuple[str, bytes]]:
    """按"最像口令"的顺序吐出候选值。

    分三档，前一档跑完才进下一档：

    1. 解引用片段开头的 32 字节。实测口令就落在 ``keyData[8]`` 所指缓冲区的
       第 0 字节，这一档通常第一个候选即命中；
    2. 其余解引用片段与主缓冲区；
    3. 未解引用的原始片段。

    每档内部再从偏移 0 起逐个偏移枚举，保证不会漏掉换版本后偏移变化的情形。

    同一轮里多个指针槽常常指向同一块缓冲区，候选值按内容去重：实测一次登录
    截获 68 段片段、42432 个候选，去重后 34466 个——每个候选都要跑 256000 轮
    PBKDF2（单次约 144 ms），删掉重复值是这一档最省时间的动作。
    """
    seen_blobs: set[bytes] = set()
    seen_values: set[bytes] = set()

    def unique(data: bytes) -> bool:
        if data in seen_blobs:
            return False
        seen_blobs.add(data)
        return True

    deref = [(s, d) for s, d in blobs if "deref" in s and unique(d)]
    plain = [(s, d) for s, d in blobs if "deref" not in s and unique(d)]

    def emit(source: str, data: bytes, offsets) -> Iterator[tuple[str, bytes]]:
        for off in offsets:
            for length in lengths:
                if off + length > len(data):
                    continue
                value = data[off : off + length]
                if value in seen_values:
                    continue
                seen_values.add(value)
                yield f"{source}+{off}x{length}", value
                stripped = value.rstrip(b"\x00")
                if len(stripped) >= 16 and stripped != value:
                    if stripped not in seen_values:
                        seen_values.add(stripped)
                        yield f"{source}+{off}x{length}[strip]", stripped

    for source, data in deref:
        yield from emit(source, data, [0])

    for source, data in deref:
        yield from emit(source, data, range(ALIGN, max(ALIGN, len(data) - 15), ALIGN))

    for source, data in plain:
        yield from emit(source, data, range(0, max(ALIGN, len(data) - 15), ALIGN))


