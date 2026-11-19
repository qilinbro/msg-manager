# Third-party notices — restricted NoneBot bridge

本集成通过独立 Python 环境使用上游包；本目录不内嵌第三方包源码或 wheels。完整 **34 个发行包**的精确版本和 wheel SHA-256 见 [Windows / CPython 3.12 依赖锁](<requirements-windows-py312.lock.txt>)；不要把下列重点归属误当成只有四项依赖，或误当成全部依赖都使用 MIT。

| 重点组件 | 锁定版本 | 上游来源 | 许可证 |
| --- | --- | --- | --- |
| nonebot2 | `2.5.0` | [nonebot/nonebot2](https://github.com/nonebot/nonebot2) | MIT |
| nonebot-adapter-telegram | `0.1.0b21` | [nonebot/adapter-telegram](https://github.com/nonebot/adapter-telegram) | MIT |
| nonebot-adapter-feishu | `3.1.0` | [nonebot/adapter-feishu](https://github.com/nonebot/adapter-feishu) | MIT |
| larkify | `0.2.0` | [StarHeartHunt/larkify](https://github.com/StarHeartHunt/larkify) | MIT；另含下述第三方 NOTICE |

本次核对的 NoneBot2 2.5.0 与飞书适配器 3.1.0 wheel 元数据声明 MIT，但未附独立 LICENSE；下文按对应固定上游 tag 补齐全文。Telegram 适配器、larkify 的许可证与 larkify NOTICE 按锁定 wheel 内文本保留。

安装和再分发要求：

- 本文件保留下列三个 NoneBot 上游项目的 MIT 原文，以及 larkify 自身的 MIT 和其 wheel 附带的完整 githubkit / oapi-sdk-python MIT 归属。
- 如果再分发 wheels、安装环境或含这些依赖的产物，必须保留每个 wheel 已附带的许可证、NOTICE、版权和发行元数据，并按各依赖的许可证补齐必要文本；不能只保留本文件、删除上游文件，或以锁文件代替许可证。
- 上游包仍属于各版权持有人。这里关闭某个上游运行功能（例如重试、回调或长连接）不构成删除其归属的理由；NOTICE 中的功能描述也不表示产品已启用该功能。
- 本文件补充重点项目的归属，不替代其余锁定依赖自身的条款或对最终分发产物的逐包合规检查。

## NoneBot2 2.5.0 — MIT

核对来源：[v2.5.0 LICENSE](https://raw.githubusercontent.com/nonebot/nonebot2/v2.5.0/LICENSE)。

```text
The MIT License (MIT)
Copyright (c) 2020 NoneBot Team

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

## NoneBot Telegram adapter 0.1.0b21 — MIT

核对文本来自该版本 wheel 附带的许可证；[上游仓库](https://github.com/nonebot/adapter-telegram)。

```text
MIT License

Copyright (c) 2021 NoneBot Team

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## NoneBot Feishu adapter 3.1.0 — MIT

核对来源：[v3.1.0 LICENSE](https://raw.githubusercontent.com/nonebot/adapter-feishu/v3.1.0/LICENSE)。

```text
MIT License

Copyright (c) 2021 NoneBot

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## larkify 0.2.0 — MIT

核对文本来自该版本 wheel 附带的许可证；[上游仓库](https://github.com/StarHeartHunt/larkify)。

```text
MIT License

Copyright (c) 2026 StarHeartHunt

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

### larkify 0.2.0 bundled NOTICE（原文保留）

```text
larkify includes code derived from the following projects, both under the
MIT License.

--------------------------------------------------------------------------------
githubkit (https://github.com/yanyongyu/githubkit)

The client core, configuration, response wrapper, exceptions, retry, throttling
and cache layers, and the design of the generated clients.

MIT License

Copyright (c) 2022 yanyongyu

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

--------------------------------------------------------------------------------
oapi-sdk-python (https://github.com/larksuite/oapi-sdk-python)

The long connection protocol handling, the event delivery checks and the
callback models.

MIT License

Copyright (c) 2023 Lark Technologies Pte. Ltd.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
