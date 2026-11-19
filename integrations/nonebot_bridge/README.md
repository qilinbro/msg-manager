# NoneBot 受限本机桥接

对应 CHG-E04-002 / CHG-T22-002。Go 仍是唯一业务核心，负责权限、人工同意、加密存储、冻结发送授权和持久账本；这里实际复用 Python NoneBot 的适配器，不是把协议另写一遍，也不部署完整 NoneBot 服务。

## 1. 固定依赖与运行前提

| 组件 | 精确版本 | 上游 |
| --- | --- | --- |
| NoneBot2 | `2.5.0` | [nonebot/nonebot2](https://github.com/nonebot/nonebot2) |
| Telegram 适配器 | `0.1.0b21` | [nonebot/adapter-telegram](https://github.com/nonebot/adapter-telegram) |
| 飞书适配器 | `3.1.0` | [nonebot/adapter-feishu](https://github.com/nonebot/adapter-feishu) |
| larkify（飞书适配器依赖） | `0.2.0` | [StarHeartHunt/larkify](https://github.com/StarHeartHunt/larkify) |

- 部署目标是 **Windows x64、CPython 3.12 x64、独立 venv**；准备脚本要求 PowerShell 7.2 或更新版本。
- [Windows/Python 3.12 依赖锁](<requirements-windows-py312.lock.txt>) 是全部 **34 个发行包的精确版本与 wheel SHA-256** 的唯一清单。不是只固定上表四个包；不允许源码包、浮动依赖或运行时自动安装。
- 第三方包不作为源码或 wheel 提交到本集成目录；归属和再分发要求见 [THIRD_PARTY_NOTICES](<THIRD_PARTY_NOTICES.md>)。安装依赖不等于授予平台权限。
- venv、解释器、桥接目录、锁和可选 wheelhouse 必须来自可信来源，由管理员控制写权限。版本检查不证明已有安装文件未被篡改，也不是供应链签名或系统沙箱验证。

## 2. 准备与只读检查

以下命令从仓库根目录执行，路径均为占位示例，不含真实账号、凭据或本机配置。准备脚本是 [prepare-nonebot-bridge.ps1](<../../scripts/prepare-nonebot-bridge.ps1>)。

```powershell
# 默认就是 CheckOnly：不创建目录、venv，不下载、不安装、不修复。
pwsh -NoProfile -File .\scripts\prepare-nonebot-bridge.ps1

# 检查一个已经准备好的、绝对本地路径的 venv。
pwsh -NoProfile -File .\scripts\prepare-nonebot-bridge.ps1 `
  -CheckOnly -VenvDirectory 'C:\trusted\msg-manager\.tools\nonebot-bridge-venv'

# 只有显式 -Install 才允许为新的/空的目标创建并安装。
pwsh -NoProfile -File .\scripts\prepare-nonebot-bridge.ps1 `
  -Install -PythonExecutable 'C:\trusted\CPython312\python.exe'

# 离线安装：wheelhouse 必须事先存在，且含锁要求的兼容 wheels。
pwsh -NoProfile -File .\scripts\prepare-nonebot-bridge.ps1 `
  -Install -PythonExecutable 'C:\trusted\CPython312\python.exe' `
  -VenvDirectory 'C:\trusted\msg-manager\.tools\nonebot-bridge-offline-venv' `
  -Wheelhouse 'C:\trusted\nonebot-wheelhouse'
```

参数和行为：

- `-VenvDirectory` 默认是仓库下 `.tools/nonebot-bridge-venv`；自定义路径必须是固定本地磁盘上的绝对路径。拒绝 UNC、设备路径以及路径/祖先中的符号链接或 junction。
- `-PythonExecutable` 是**新建 venv 时**使用的可信 CPython 3.12 x64 可执行文件，不是 worker，不通过 PATH 猜测，不安装全局包。
- 不带 `-Install` 与显式 `-CheckOnly` 等价；两个开关不能同时使用。缺少 venv、版本不符、依赖冲突均返回非零，不尝试自动修复。
- 只读检查验证解释器实现/版本/位数、venv 前缀归属、禁用 system site-packages、34 项精确安装版本及 `pip check`。它只读依赖元数据，不导入适配器、不启动 worker、不读取产品凭据或数据库、不连接任何平台。
- `-Install` 只向新/空目标安装。已存在完整 venv 时仍只检查；已有目录不完整或版本不符时拒绝覆盖，不重装、不升级、不删除。失败产生的全新不完整 venv 保留给管理员人工检查。
- 安装固定使用 `pip --isolated --disable-pip-version-check install --require-virtualenv --no-input --no-cache-dir --retries 0 --timeout 30 --require-hashes --only-binary=:all: --no-deps -r <lock>`。默认唯一索引是 `https://pypi.org/simple`；`-Wheelhouse` 改用 `--no-index --find-links <existing-directory>`，不会在线补包。
- 不升级 pip；venv 的 pip 来自所选 CPython 自带引导。子进程不继承凭据、代理、CA 覆盖或 pip 索引配置，禁用 pip 配置文件；不使用 shell、安装脚本、sdist 构建或交互提示。标准输出/错误直接丢弃，只报告固定安全状态，不回显异常、环境或平台响应。
- 准备完成**不会激活产品运行时**，不修改服务配置、不启动/重启服务，也不启用接收循环、发送或计划。

## 3. 管理员运行配置

这些是 **Go 服务进程**的管理员配置，不是浏览器或模型可选择的参数。解释器和 worker 必须成对设置，并指向同一套已审查代码和已检查依赖。以下只演示非凭据路径配置：

```powershell
$env:SOCIALD_NONEBOT_PYTHON = 'C:\trusted\msg-manager\.tools\nonebot-bridge-venv\Scripts\python.exe'
$env:SOCIALD_NONEBOT_WORKER = 'C:\trusted\msg-manager\integrations\nonebot_bridge\bridge.py'
$env:SOCIALD_NONEBOT_TIMEOUT_SECONDS = '25'
$env:SOCIALD_FEISHU_TRANSPORT = 'nonebot'
```

| 环境项 | 约束 |
| --- | --- |
| `SOCIALD_NONEBOT_PYTHON` | 可信 venv 内 Python 的绝对本地 `.exe` 路径。 |
| `SOCIALD_NONEBOT_WORKER` | 已审查的 [bridge.py](<bridge.py>) 的绝对本地 `.py` 路径；与 Python 成对提供，不能只配其中一个。 |
| `SOCIALD_NONEBOT_TIMEOUT_SECONDS` | 可选整数 `1..60`，默认 `25` 秒；桥接未配置时不要单独设置超时。 |
| `SOCIALD_FEISHU_TRANSPORT` | 未设置或 `nonebot` 都选择 NoneBot，**即使桥接尚未配置也不回退**。只有管理员显式设置 `legacy-go` 才选择旧 Go 发送器。 |
| `TELEGRAM_BOT_TOKEN` | 仅 Go 管理端持有的机器人 token；其数字前缀必须精确等于预期 bot ID。不在示例、日志、命令历史、仓库或模型中放实际值。 |
| `TELEGRAM_BOT_ID` | 预期机器人 ID；规范正十进制字符串，无前导零、正负号，最大 `2^52-1`。 |
| `TELEGRAM_BOT_ALLOWED_PRIVATE_CHAT_IDS` | `1..20` 个互不重复的、精确匹配的正私聊 ID，用英文逗号分隔；无空格、空成员、尾逗号、前导零或群组负 ID；每项最大 `2^52-1`。 |
| `TELEGRAM_BOT_LIMIT` | 可选整数 `1..100`，每批默认 `20` 条更新，不是后台轮询频率。 |

Telegram 的 token、预期 bot ID、私聊白名单必须完整配置，否则拒绝；全部不配才表示未配置。凭据应按受控服务环境的运维流程注入 Go，不向本示例追加实际值；不要用 NoneBot 配置文件或 `.env` 传给 worker。飞书凭据继续由原有 Go 授权/凭据边界取得，不新增浏览器或模型凭据入口。

状态中的 `available` / `Available` 只表示桥接路径已配置，不证明 Python/依赖已经检查、网络可达、凭据有效、账号归属正确或平台授权已经核验。必须分别完成只读依赖检查和经授权的身份/权限核验，不能以路径存在代替真实验收。

**不存在自动兼容回退。** NoneBot 未配置、缺包、失败或超时都不能触发旧 Go 发送器。切换 `legacy-go` 是管理员明确的兼容选择，不是重试或绕过 unknown/平台策略的办法。

## 4. 仅三个固定动作

[桥接入口](<bridge.py>) 只接受一次版本为 `1` 的严格 JSON 请求，顶层仅 `version`、`action`、`payload`。请求最多 `64 KiB`，响应最多 `4 MiB`，一次操作后进程退出；不开放通用 SDK 方法名、URL、命令、插件或脚本参数。

| 动作 | 唯一用途与上游调用 |
| --- | --- |
| `telegram.probe` | 实际调用 Telegram 适配器的 `getMe`、`getWebhookInfo`，核对预期 bot 身份并拒绝已有 webhook；不取消息。 |
| `telegram.poll` | 同样重新核对身份/webhook，然后仅一次 `getUpdates`，使用 Go 根据持久游标租期选定的 offset、明确上限及 `timeout=0`；不自动循环或重试。 |
| `feishu.send` | 实际使用飞书 `Bot.call_api` → `Adapter._call_api` → larkify 请求路径，按冻结的本人 `open_id`、正文、UUID 发送一条文本；不支持任意目标或任意 API。 |

Python 不运行 `nonebot.init()`、插件发现、适配器启动/关闭生命周期、默认 echo、webhook 服务、长连接或默认 polling task。虽然锁内有 `python-dotenv`，这里不加载 `.env`、HOME 配置或环境 BaseSettings；传入对象由受控代码构造。没有 `deleteWebhook`、Telegram 发送、飞书接收、联系人搜索或个人号登录动作。

## 5. Telegram：每批同意与事务游标

- 这是 **Bot API 机器人**，不是 MTProto 个人号，不会取得个人账号原有私聊历史。身份核验成功也不等于持续在线、已允许后台接收或允许发送。
- 每次手动接收**每一批**都须新的人类明确同意；后端接收请求只接受本次 `{"consent_receive":true}`，不存成长期授权，不允许前端提交 token、白名单、offset 或任意 SDK 参数。
- 每批重新核对 `getMe` 的真实 bot 身份与预期 ID，并检查 webhook。发现非空 webhook 即 `webhook_present`：停止，交管理员人工处理；**绝不静默删除或替换 webhook**。
- 只接收白名单内、发送者与私聊 ID 一致的普通人类文本。群/频道、编辑、媒体、其他不支持事件或未授权私聊只保留有界事件标识和忽略原因，不把其正文、昵称、附件或引用历史传入产品消息结果。
- Bot API 本身可能返回其他更新；白名单是桥内和 Go 层的过滤/验证，不是宣称平台在网络传输前按白名单过滤。合成测试不证明真实账号归属或平台授权。
- Go 把可接收消息、事件去重记录、忽略原因及新游标放在**同一加密存储事务**中提交；提交失败或生命周期取消不能把下一 offset 当作已提交。只有下一次人工同意才会再读一页，没有提前 ACK 的上游默认循环。
- **闲置恢复不是放宽 ID 校验。** [Telegram 更新规则](https://core.telegram.org/bots/api#update)允许至少一周无新更新后随机选择 update ID；旧 offset 不能永久当作单调水位。Go 以最近一次**非空页面成功提交**时间维护24小时保守租期，空轮询不续期。租期过期、时间缺失/非法/在未来时，在平台请求**之前**选 `offset=0`，仅取本次同意的一页；不先用旧 offset 试探，不加第二次请求/自动重试，Python/Go 仍严格验证本次实际 offset。
- 重置时，事件去重使用新 epoch，消息仍按 bot/私聊/message ID 跨 epoch 稳定去重；epoch、revision、消息及游标同事务。精确快照和单调 revision 的 CAS 阻止旧页面在数值 offset 重现时误提交（ABA）。重启/重新核验不抹除这些状态；重置失败/取消/过期边界冲突均不推进新游标。
- [平台待收队列最多保留24小时](https://core.telegram.org/bots/api#getting-updates)。`offset=0` 只能读取仍待收的更新，不能恢复已过期消息、个人号历史或其他消费者已确认的数据；本桥接没有后台轮询，不承诺闲置期间零缺口或跨平台 exactly-once。
- 同一 bot 不应再由其他程序同时消费 `getUpdates`；外部消费者、手工修改 offset、删除 webhook 都不在本桥接的事务保证内。本地事务和去重不是跨平台 exactly-once 承诺。

## 6. 飞书：冻结发送与 unknown 不重放

- 仍限现有“固定本人早晚报”的 Go 授权路径：目标、正文、UUID 先冻结；规则、持久 claim、发送窗口、取消和 unknown 恢复均由 Go 管理。不能通过 worker 或模型新增任意消息发送能力。
- 仅允许现代 OAuth `https://accounts.feishu.cn/oauth/v3/token`，`grant_type=client_credentials`、精确 scope `im:message:send_as_bot`；消息路径固定为 `https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id`。
- 每次至多一次 token 请求及一次消息请求，不使用旧 tenant-token 鉴权补救、不刷新认证后重放、不跟随重定向、不读取环境代理、不自动网络/SDK重试。真实 SDK 仍经过固定请求/响应门禁；不是用“换 SDK”逃逸策略。
- `not_sent / attempted / responded / unknown` 是内部阶段事实，不是可重试建议。只有明确证明消息没有发出才能算已知未发送；启动后超时、崩溃、响应丢失或格式不可信要保守处理，无法确认就保留 `unknown` 并人工核实。
- **unknown 绝不自动重发。** 不换 UUID、不换 transport、不换鉴权端点重放，不因 token 过期/429/5xx 做补发；UUID 和本地账本也不是平台 exactly-once 证明。
- 飞书错误 **21001 是平台/管理员策略限制**；接入 NoneBot 不会解除它，不能绕过、降低 scope 或冒充其他身份。必须由有权限管理员按平台流程处理，真实发送仍需另行授权和验收。
- 桥接构造的 SDK bot 信息不是远端身份核验；本地合成成功不证明真实飞书账号、授权归属、实时可用或送达。

## 7. 安全边界与验证范围

**受限 stdio 子进程不是 OS 沙箱。** `-I` 和独立 venv 隔离 Python 搜索路径与包环境，不撤销当前用户的文件系统/网络权限。worker/第三方代码仍有启动它的用户所拥有的文件权限；不可信的解释器、包或可写路径可能绕过代码门禁。需要独立 OS 用户、ACL、容器或防火墙时，必须另外设计、审查和部署，不得用本桥接的名字代替系统隔离。

Go 不经过 shell，固定以 Python `-I -u -B` 启动已配置 worker。子进程仅有运行必需的 `SystemRoot/WINDIR/TEMP/TMP` 和收窄的 PATH；不继承产品/模型凭据、HOME、Python 路径、环境代理或 CA 覆盖。凭据和冻结请求仅通过有界 stdin 传入，不进 argv；stdout 仅承载严格协议，stderr 不回传原始平台/Python异常。可信实现禁环境代理、重定向、默认生命周期和所有自动重试，并限制固定 HTTPS 端点；这些都是代码约束，不是恶意代码无法联网的系统证明。

本地检查与合成测试可用已准备好的 venv 执行：

```powershell
# 可选仓库门禁：先调用只读 CheckOnly，再运行实际包的离线合成单元测试。
pwsh -NoProfile -File .\scripts\check.ps1 -IncludeNoneBot `
  -NoneBotVenvDirectory 'C:\trusted\msg-manager\.tools\nonebot-bridge-venv'

# 单独运行桥接合成测试；使用测试替身，不填真实配置。
& 'C:\trusted\msg-manager\.tools\nonebot-bridge-venv\Scripts\python.exe' `
  -I -u -B -m unittest discover -s integrations/nonebot_bridge -p 'test_*.py' -v
```

版本/依赖检查和模拟平台测试不连接真实平台或付费模型，不读取生产凭据/聊天库，不发送、不启用计划、不部署/重启现有实例。它们不能替代指定非作者技术审查、真实平台授权、真实 UI/原生验收或 kirinbao 的最终验收。微信自动回复及定时发送的暂停状态不因本集成而改变。
