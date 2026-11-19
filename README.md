# Social Intelligence OS

多平台社交关系助手。当前实现为 **统一消息中心 + QQ Milky 账号核验与协议同步 + 微信本地库读取 + 邮箱只读同步 + AI 分类与联系人档案 + 原生工具调用 Agent**：独立 Go 后台、加密本地存储、白色简约 React 工作台与 Tauri 外壳。

历史 main 整合的检查与同步记录见 [main整合记录](<docs/acceptance/main-integration.md>)。本功能分支新增的 MCP、持久本机连接、白色工作台与 Agent 以各自验证记录为准，不因此视为已合入 main 或已更新正在运行的服务；正式审查和产品验收状态继续单独登记。

## 当前能运行的内容

- 本机 HTTP 服务、主令牌与可撤销设备鉴权、来源校验、健康检查和统一错误结构。Windows 启动器/MCP 共用 DPAPI 受保护的稳定服务凭据；浏览器可记住设备 90 天，不持久化主令牌，见 [持久连接说明](<docs/adr/0013-persistent-local-connections.md>)。
- 读取真实服务状态，QQ 个人号仅展示 Milky 连接器；微信 Clawbot / Telegram 保留待接入声明。
- QQ、微信、Telegram、闪聊的导入文本在同一消息中心管理：平台筛选、搜索、未读、置顶、归档、会话详情与历史分页。
- 用户确认后导入统一 JSON，支持 Telegram Desktop 单个个人私聊导出；稳定标识去重、冲突整批回滚，SQLCipher 加密保存。
- QQ Milky 1.3 协议端可核验本人账号与版本、按联系人同步好友私聊历史、显式开启后台接收；加密入库去重、缺口显式记录并有界补查。发送保持关闭，真实账号验收待进行，见[QQ Milky 接入说明](docs/qq-milky-setup.md)。
- 当前会话可先预览确认，再调用 DeepSeek/兼容接口整理摘要、风险、草稿和待核实记忆；用户确认记忆后才进入后续上下文。真实模型与界面验收待进行。
- **Agent 助手**通过原生 `tools/tool_calls` 调用已实现的业务 API，展示实际工具回执、不可变操作预览和 unknown；每轮重新同意模型数据外发，写入及有副作用的核验逐项人工确认。人工凭据/模型设置与未实现接口不开放执行，不提供任意主机/文件/SQL 权限。见 [助手使用与边界](<docs/assistant.md>)。
- 工作台采用白底、灰字、细线和紧凑导航，不添加渐变、光晕或装饰统计；保留原业务入口、持久设备授权和页面内未提交 AI 草稿。官方 modern-web-guidance 按 [固定来源](<.agents/skills/modern-web-guidance/PROVENANCE.md>)安装为项目级技能，非 DSH 全局插件。
- 联系人档案支持人工资料、AI 分类/摘要、事实候选、证据来源及 Markdown 导出。明确授权后可后台自动整理新消息、按公开姓名/公司搜索资料；公开结果待核实后确认，默认不开自动整理或联网。模型与 Tavily 密钥只在本机配置，见[档案配置](docs/ai-profiles-setup.md)。正式审查和真实模型/搜索验收待进行。
- 设置页可保存模型地址、名称、密钥和每日额度，密钥本机加密保存、不回显。导入可明确授权逐批分析全部已入库文本，显示处理进度，并把昵称、平台ID和聊天证据归入原联系人档案；同一人的会话经用户确认后可关联、解除及导出联合文档。见[使用与边界](docs/ai-profiles-setup.md)。
- 参赛叙事按“盯 + 跑”落地：后台持续监看授权来源，长时分批整理全历史；用户回来看到动作、证据、进度、失败/unknown 和待办，而不是一句“已完成”。动作清单、身份卡、回执和五分钟演示路径见[不在场工作模式](docs/competition/不在场工作模式.md)。
- 每日摘要默认上海时区 08:00，可设置时间、启停或立即生成；立即运行和周期计划分别明确同意当前模型/数据/费用，保存模型配置会撤销周期同意。只分析过去 24 小时未暂停、已入库的有界私聊/群聊文本，逐会话调用模型后本地聚合证据；群成员/@归属和权限未建模。同日 running/succeeded/failed/unknown 均不重发，结果只在工作台显示，与分析/档案/Agent 共享持久额度。见 [日报决策](<docs/adr/0011-daily-digest-and-relevance.md>)；[首轮验证记录](<docs/acceptance/T20-daily-digest-validation.md>)保留历史结果，不代替本轮检查。
- 邮箱支持本机配置 IMAP TLS 授权码后手动同步最近邮件，显示主题与地址；不改原邮箱已读，不发信。先支持 QQ/163/授权码企业邮箱的协议能力，真实账号尚待核验。
- 微信支持配置本机4.x数据库和密钥后读取明确选定的私聊/群聊文本及关键词筛选，或按授权会话开启后台定时只读、去重入库；默认关闭，设置加密保存，关闭页面后后台可继续。仍是最近记录窗口，不保证全历史或离线补漏，不含群成员/@归属与群权限模型。配置见[微信本地记录接入](docs/wechat-db-setup.md)，真实私聊/群聊验收待进行。
- 微信4.1.15.56另有用户主动确认的一次性本机初始化入口 `scripts/initialize-wechat.ps1`，在重新登录时临时挂钩指定进程，密钥验证后DPAPI保存。用户已回传真实初始化成功日志；后台工作台读取另行验收，依赖准备和操作步骤见同一接入说明。

自动发送始终关闭。QQ Milky 可在明确授权后开启后台接收，微信可对明确选定的会话开启本地库周期读取；QQ 可靠历史水位、Telegram 在线授权、通用任务/发件箱恢复、Windows服务安装和真实消息发送仍未实现。电脑及后台需要保持运行；邮箱仍按需同步，不表示各平台都已持续在线。QQ 个人号统一使用 [Milky 接入说明](docs/qq-milky-setup.md)，邮箱见[接入说明](docs/email-setup.md)；凭据仅在后台配置。旧 OneBot 环境变量与核验入口已退役，迁移说明见[历史入口](docs/qq-onebot-setup.md)。

QQ、飞书、微信和 Telegram 的准备条件、登录/授权入口、首次验证与恢复步骤统一见[平台登录与部署指南](<docs/platform-login-deployment.md>)。先确认实际运行版本与数据根；Telegram 个人号实现另在功能分支，不代表当前实例已有登录入口。

全新安装可按[统一消息中心使用说明](docs/unified-inbox.md)使用[启动器](<scripts/start-inbox.ps1>)准备当前 Windows 用户保护的数据库密钥，再启动前端、连接服务和导入记录；已有实例不要重复启动，代码根与原数据根分离时先按上述部署指南核对。源码不含预置消息或真人聊天数据。

## 本地启动

需要 Node.js 24、Go 1.26+、GCC（SQLCipher CGO 构建）和 PowerShell 7.2+。当前开发环境可使用项目内 `.tools/go` 的便携 Go，工具本身不进入仓库。

`initialize-wechat.ps1`、`prepare-wechat-init.ps1`、`configure-wechat.ps1` 和 `start-inbox.ps1` 可从 Windows PowerShell5.1 启动，会自动调用已有的 PowerShell7.2+ x64。实际业务仍在7运行，不自动安装或修改执行策略。

### 1. 启动独立后台

统一消息管理在项目根目录打开 PowerShell 7：

```powershell
.\scripts\start-inbox.ps1
```

普通启动复用当前 Windows 用户 DPAPI 保护的服务凭据，默认不打印令牌、不改剪贴板。首次/到期后授权浏览器时，将上述命令改为 `.\scripts\start-inbox.ps1 -CopyApiToken`；这是显式复制操作，剪贴板管理器可能保留历史。首次升级请先正常停止旧后台，再更新构建并启动，不自动接管已有实例。

如果只查看骨架与 QQ 核验，可不配置数据库，以原入口启动（仅页面内存连接，不支持设备持久授权，消息中心会提示先配置加密存储）：

```powershell
$env:SOCIALD_API_TOKEN = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$env:SOCIALD_ADDR = '127.0.0.1:17890'
$env:SOCIALD_API_TOKEN | Set-Clipboard
.\scripts\dev-backend.ps1
```

保持终端运行。上面的开发模式示例显式复制的是本次 env token，与默认受保护凭据路径不同；每次生成新值都会让旧授权失效。推荐使用普通启动器：首次在工作台确认记住设备后，刷新/关闭重开或正常后台重启无需重复复制，授权默认 90 天，可在设置中忘记。只保存不可导出设备私钥，主令牌/短 bearer 不进入浏览器存储、URL 或日志。未勾选记住、旧后台或安全存储受限时只保留当前页面内存，刷新后重新输入；旧 sessionStorage 主令牌会清除。

临时断网/503 保留设备身份，恢复只校验状态，不重放导入/同步/分析。主动忘记会尽力撤销，离线时远端撤销未确认；主令牌轮换、真实监听地址变化、到期或站点数据清理后需重新配对。MCP 默认客户端只配可执行文件路径，不内嵌 token；见 [MCP 接入](<docs/mcp-setup.md>)。电脑和后台仍须运行，未安装开机服务。

### 2. 启动前端

另开终端：

```powershell
cd apps/desktop
npm ci
npm run dev
```

打开 `http://127.0.0.1:1420`，首次在设置页填写主令牌、确认“记住这台可信任的电脑（90 天）”并连接；之后在同一浏览器来源自动恢复，无需反复粘贴。未记住则仅当前页面有效。健康检查地址是 `http://127.0.0.1:17890/health/live`。

`/health/ready` 当前仍返回 **503 NOT_READY**，因为无人值守所需实时接收、通用任务与发送链路尚未完成；本地导入管理能力由 `/api/v1/inbox` 的 `storage_enabled` 单独声明。

### 3. 桌面外壳

Tauri 源码位于 `apps/desktop/src-tauri`。原生启动还需要 Rust/Cargo、Windows C++ 构建工具与 WebView2；本轮未在缺少这些环境的机器上验证原生编译。

```powershell
cd apps/desktop
npm run tauri dev
```

Go 后台需独立启动。Tauri 不以 sidecar 启停它；Windows 服务注册与自动恢复由 T19 后续实现。

## 检查

安装前端依赖后，在根目录运行：

```powershell
.\scripts\check.ps1
```

检查 Go 格式、测试、静态检查、API 引用、前端类型与构建；不自动声称桌面编译通过。已有 Rust 环境时可加 `-IncludeDesktop` 验证 Cargo 构建检查。

## 架构与文件

```mermaid
flowchart LR
    UI[React / Tauri 工作台] -->|本机 Bearer API| API[Go sociald]
    API -->|Milky 1.3 核验/历史/事件| MILKY[QQ Milky 独立协议端 GPL-3.0]
    MILKY --> INBOX
    API --> DOMAIN[领域 Scope / 消息 / 决策接口]
    API --> INBOX[统一导入 / 会话 / 本地状态]
    INBOX --> STORE[SQLCipher / SQLite]
    API -->|手动只读同步| EMAIL[IMAP TLS 邮箱]
    EMAIL --> INBOX
    API -->|预览确认后单次分析| AI[固定 DeepSeek / 兼容模型]
    API --> ASSISTANT[原生 Agent / 有界进程内运行]
    ASSISTANT -->|tools / tool_calls| AI
    ASSISTANT -->|固定业务分派 / 逐项确认| DOMAIN
    AI -->|校验结果| MEMORY[限域记忆 / 摘要 / 本地草稿]
    MEMORY --> STORE
    DOMAIN -. 后续实现 .-> CONNECTOR[微信 Clawbot / Telegram]
    API --> JOBS[档案持久作业 / 租约 / 配额]
    JOBS --> AI
    JOBS --> SEARCH[Tavily 公开搜索]
    SEARCH --> CANDIDATE[待核实来源候选]
    CANDIDATE --> STORE
    DOMAIN -. 后续实现 .-> AGENT[通用任务 / 审批 / 发件箱]
```

- [接口契约](<api/openapi.yaml>)：JSON 语法编码的 YAML 1.2，OpenAPI 3.1；消息管理、Milky、邮箱、AI/档案、日报及 5 项 Assistant 控制接口。助手目录覆盖原有 62 项（44 可执行、5 人工专用、13 未实现）；发送与通用审批仍不可用。
- [Agent 助手](<docs/assistant.md>)：[原生工具与确认设计](<docs/adr/0014-native-assistant-and-confirmed-business-tools.md>)、模型同意、权限目录、只读恢复与 unknown 边界。
- [QQ Milky 接入](docs/qq-milky-setup.md)：本机协议端配置、核验、历史同步、后台接收、已知限制与真实账号验收清单。
- [统一消息中心](docs/unified-inbox.md)：四平台导入格式、启动、数据保护和在线能力边界。
- [本地 MCP 服务](docs/mcp-setup.md)：通过 stdio 查询消息、导入指定微信会话，含客户端配置与数据边界说明。
- [自动获取聊天记录选型](参考项目/自动同步/README.md)：减少手工导入的QCE、Telegram个人账号及微信方案核验；目前为调研与待实施步骤。
- [邮箱接入](docs/email-setup.md)：QQ/163/企业邮箱本机配置、只读同步与限制。
- [联系人档案接入](docs/ai-profiles-setup.md)：后台自动分类、公开来源候选确认、配额及导出；[架构决定](docs/adr/0009-ai-contact-profiles.md)。
- [AI 管理接入](docs/ai-setup.md)：DeepSeek/兼容服务本机配置、预览确认、记忆核对与草稿。
- [首轮架构决定](docs/adr/0001-initial-architecture.md)：工程范围、权限与未实现边界。
- [个人账号适配决定](docs/adr/0003-personal-account-adapters.md)：QQ 个人号仅使用 Milky；保留历史决策及迁移边界。
- [技术文档](技术文档.md)、[逐项任务](任务规划.md)、[feat 进度](feat.md)。
- [参考项目](参考项目/README.md)：上游源码仅用于静态参考。
- [本地交付与待办](docs/acceptance/T04-handoff.md)：源码包生成方式、GitHub 同步状态和剩余验收条件。
- [全功能测试清单](<docs/acceptance/T22-all-features-test-plan.md>)：[测试前修复与工程验证](<docs/acceptance/T22-pretest-validation.md>)；按连接→消息/来源→联系人→Agent→飞书→设置逐项测试，区分合成回归、真实授权、占位/暂停项与未合并分支。

## 三人分工

- angjustinl：后端、AI、数据与后台服务。
- joey：前端、UI、桌面与 Tauri。
- kirinbao：产品、测试验收与交付。

前后端由非作者交叉审查；辅助审查和构建通过不替代指定成员的正式审查与验收。
