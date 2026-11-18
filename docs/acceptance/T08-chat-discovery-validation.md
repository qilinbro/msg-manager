# T08 / T10｜私聊群聊、关键词搜索与上下文 Agent 工程验证

- 日期：2026-11-18（Asia/Shanghai）。
- 关联：FEAT-T08 / CHG-T08-008、FEAT-T10 / CHG-T10-004。
- 交付分支：`feat/mcp-service`；本轮基线为已存在的 [7019719](https://github.com/qilinbro/msg-manager/commit/70197190d0f1fe9f042e53f9cae62e1ebdc5c033)。当前提交哈希不提前编造。
- 状态：**实现、最终全检、非缓存 Store/API race、最后一次独立构建及桌面/390px隔离 Chromium 工程验证通过**；40项断言、74个已拦截API、0 mutation、0真实模型/平台调用。指定非作者审查、真实账号业务/模型及原生 Tauri 验收仍待进行，实际完成日期为 **—**。工程通过不等于已上线或真实验收通过。
- 指定职责：joey 负责前端/UI，angjustinl 负责后端/AI；后端由 joey 审，前端由 angjustinl 审，测试与交付脚本由非作者技术成员审查，kirinbao 组织验收。开发代理辅助复核不代表任何人员批准。

## 1. 实际交付

### 1.1 明显的私聊 / 群聊与搜索入口

- 消息页顶部提供独立“私聊”“群聊”标签，位于列表内部滚动区之外；两个标签支持方向键、Home / End 和 roving tabindex。默认显示私聊；类型与平台、分类、收件箱视图、关键词共同过滤，不把私聊和群聊结果混在一起。
- “关键词匹配搜索”是有标签、可清除的可见输入，搜索名称、平台 ID、账号及已入库正文等已有字段；切换类型和关键词会清除旧选择，迟到响应、后台刷新及旧分页不能覆盖新条件。
- 真正的历史正文命中单独显示“正文命中”，不替换“最近一条”。只有名称等元数据命中时不伪造正文片段。片段来自同一会话、同一工作区的实际原文，至多 376 个 Unicode 码点；高亮使用文本节点而非 HTML / 动态正则。
- 会话详情提供“当前会话正文关键词”“搜索正文”“查看全部消息”；仅搜索正文，返回匹配历史页，而不是只在当前已显示页做本地查找。选择列表结果会带入当前关键词，元数据命中但正文没有匹配时明确显示空状态。
- 微信来源面板同样提供私聊 / 群聊标签及名称 / ID 本地筛选。会话元数据只有显式点击“读取会话列表”才读取；本地筛选不向无查询参数的 sessions 接口附加参数。切换条件后，隐藏的选中会话会清除，“读取并保存消息”及监测操作不能继续作用于隐藏对象。
- 来源读取的正文条件只作用于本次读取窗口；未入库内容不可能被统一消息历史检索找到。页面提供读取微信、同步邮箱及清除筛选入口，不声称自动补齐所有平台历史。

实现见 [Inbox](<../../apps/desktop/src/Inbox.tsx>)、[WechatPanel](<../../apps/desktop/src/WechatPanel.tsx>)、[共用标签](<../../apps/desktop/src/ConversationKindTabs.tsx>)、[字面高亮](<../../apps/desktop/src/keyword-search.tsx>) 与 [使用说明](<../unified-inbox.md>)。

### 1.2 服务端真实限域检索

- 会话列表支持可选 `conversation_kind=private|group`；省略保留旧的全部类型行为，空值、非法值或重复参数返回 400。
- 列表和会话消息支持可选 `q`；有效 UTF-8、原始长度至多 256 字节、不得含 NUL 或重复参数；空白规范化后视为无搜索。
- 使用参数化 `instr(lower(...), lower(?))` 字面匹配：ASCII 大小写不敏感，中文等非 ASCII 按字面匹配，`%`、`_` 不是通配符。不伪称语义检索或 Unicode 全量大小写折叠。
- 消息搜索只匹配正文；最新匹配页按时间正序展示，上一页游标继续查询更早的匹配消息。会话类型、关键词、会话、工作区和数据版本按各接口绑定游标；改变条件不能沿用旧游标，省略新参数时保留旧接口指纹兼容。
- `matched_message` 只在真正存在正文命中时返回，不覆盖真实 `last_message`；工作区、平台、类型、分类和视图约束保持相交。
- Agent 的 `list_conversations` 和 `get_conversation_messages` 暴露相同 kind / q 能力，实际 dispatcher 调用业务处理器；参数严格校验，不提供任意 URL 或 SQL。AI 暂停仍阻止 Agent 读取来源；普通人工读取不因 AI 暂停被错误禁止。

实现与契约见 [仓储查询](<../../internal/store/query.go>)、[人工 API](<../../internal/api/inbox.go>)、[Agent 目录与分派](<../../internal/api/assistant_catalog.go>)、[OpenAPI](<../../api/openapi.yaml>)。

### 1.3 显式 Agent 与受控求助

- 顶栏“Agent 助手”、侧栏 Agent 入口及页面“找不到？让 Agent 帮我”打开原生模态工作区；原业务页面保持挂载，关闭 / Escape 返回原位置，保留筛选、会话、输入及未保存草稿。关闭不等于取消已提交的运行，也不重放请求。
- 页面求助先展示可检查的任务和上下文；“确认带入草稿”仅可填入空草稿，“追加到现有草稿”显式合并，不覆盖用户内容，不自动提交或默许模型调用。
- 收件箱上下文仅允许类型、平台、分类、视图、关键词、选中会话 ID 及会话正文关键词；微信来源同样使用最小条件。不自动带入消息正文、会话标题、平台账号、凭据、错误回显或其他业务草稿。用户主动输入的关键词仍可能敏感，实际发往模型前必须独立同意。
- 已知未决 / 不确定运行会阻止带入新任务；恢复仅使用已有 run_id 的 GET，不盲目重放 mutation。
- 本轮统一求助合并、输入界面、提交 handler 与实际 API transport 的消息校验：完整 UTF-8 至多 8000 字节，不含 NUL，不按 UTF-16 字符数判定或静默截断；错误有可见说明和 ARIA 关联。专项边界结果见下方最终检查记录。
- 保留模型目标 / revision 绑定同意、逐项不可变写入确认、独立平台发送授权、单并发和共享日额度，不因“让 Agent 帮我”扩大凭据或发送权限。当前目录为 72 项：52 可执行（50 业务 + 2 健康）、7 人工专用、13 未实现；另 5 个 Assistant 控制不递归提供给模型。

见 [App 工作区](<../../apps/desktop/src/App.tsx>)、[AssistantPanel](<../../apps/desktop/src/AssistantPanel.tsx>)、[纯校验 / 求助合并](<../../apps/desktop/src/assistant-help.ts>)、[实际助手客户端](<../../apps/desktop/src/assistant-api.ts>) 与 [助手说明](<../assistant.md>)。

## 2. 工程检查

| 检查 | 最终实际结果 | 范围 / 证据 |
| --- | --- | --- |
| 新后端检索 / API / Agent 专项 | 通过 | [Store 回归](<../../internal/store/chat_search_test.go>) 6 项、[人工 API 回归](<../../internal/api/chat_search_test.go>) 3 项、[Agent 回归](<../../internal/api/assistant_chat_search_test.go>) 4 项 |
| Store / API 非缓存 race | 通过，退出 0，32.337745s | `go test -race -tags sqlite_fts5 ./internal/store ./internal/api -count=1`；Store 25.644s、API 23.425s；[日志](<artifacts/2026-11-18-chat-discovery/store-api-race.log>) |
| 消息发现 / 微信监测组件专项 | 通过 | [实际客户端及组件测试](<../../scripts/test-chat-discovery.mjs>)、[既有监测回归](<../../scripts/test-wechat-monitor.mjs>)；严格 synthetic fetch，未声明请求会失败 |
| 助手 UTF-8 / NUL、飞书、设备授权兼容 | 通过 | [求助回归](<../../scripts/test-assistant-help.mjs>) 9 组、[助手客户端](<../../scripts/test-assistant-client.mjs>)及既有飞书/设备 31 组/内存会话 11 组；只补真实依赖，不删旧断言 |
| 最终全仓工程检查 | 通过，退出 0，32.192030s | [check.ps1](<../../scripts/check.ps1>)覆盖 Go/ vet/契约/合成脚本/Prettier/tsc/Vite；[最终日志](<artifacts/2026-11-18-chat-discovery/fullcheck.log>) |
| 最后一次独立生产构建 | 通过，退出 0，1.209998s | 51 模块，Vite 135ms；[构建日志](<artifacts/2026-11-18-chat-discovery/build.log>) |
| 冻结构建桌面 / 390px Chromium | 通过，退出 0，16.705110s | 40 项断言、74 个已拦截 API、0 mutation；[报告](<artifacts/2026-11-18-chat-discovery/report.json>)、[日志](<artifacts/2026-11-18-chat-discovery/browser.log>)；见下节 |

全检中的 Go 单测命中缓存，不冒称新的非缓存运行；单独 race 使用 `-count=1`。唯一非失败构建警告为 JS 501.98kB 超过 Vite 500kB 提示线（gzip 148.28kB），未因该提示额外拆包或视觉微调。首次全检 50.619s / 独立构建 1.278s 也保留为[先前全检](<artifacts/2026-11-18-chat-discovery/before-tab-cycle-fullcheck.log>)和[先前构建](<artifacts/2026-11-18-chat-discovery/before-tab-cycle-build.log>)，不替代这里的最终结果。

### 检查中发现并修复的问题

1. 首次整合全检在档案测试失败：既有合成会话缺少新必需字段 `conversation_kind`，实际客户端按契约拒绝。已补齐真实字段而非放宽响应校验；独立档案测试通过，最终整合全检结果另记。
2. 辅助复核发现助手此前按 JS UTF-16 长度计算 8000 上限，而后端按 UTF-8 字节拒绝。此次统一纯检查、UI、合并和 API transport，并增加中文、emoji、8000 / 8001 字节、NUL、过期 handler 和绕过按钮禁用的边界断言；在 fetch 之前拒绝不合法输入。
3. OpenAPI 旧措辞误将 Agent 的 AI 暂停约束扩展到普通人工消息读取；已按实际权限边界澄清，未放宽 Agent 权限。
4. 共享检查器引入后，相关合成 VM 加载器只补齐真实模块依赖 / TextEncoder，不删除或跳过已有飞书、设备授权、助手回归断言。

## 3. 有界实际浏览器验证

### 方法与范围

- 执行生产构建的实际静态字节，无替代应用服务器；新建临时 Chromium profile，不读已有用户 profile、浏览器存储或真实聊天。
- 应用 HTTP 请求在 Fetch request stage 全部合成 fulfill / fail，绝不 `Fetch.continueRequest`；未知地址、方法、路径、参数均失败。禁用 worker / service worker 注册及绕过、WebSocket / EventSource / WebRTC、弹窗和下载。这里不是操作系统级网络防火墙声明。
- 仅使用 synthetic-memory token、合成会话 / 消息和三个代表性合成工具目录项，不调用真实模型 / 平台，不启用监测或计划。此浏览器批次不验证真实 Agent 工具执行，实际后端工具路径由 Go 回归单独验证。合成会话unread_count=0，零mutation只描述本场景，不表示点开真实未读不会更新既有本地已读标记。
- 同一批次覆盖桌面 1440×1000 与 390×844：标签和搜索可见性、列表外布局、原文命中与最新预览分离、详情正文 / 空结果、键盘切换、原生鼠标 / 触控、Agent 预览和显式带入 / 追加、原生模态底页focus拒绝及Tab/Shift+Tab轨迹 / Escape / 焦点返回、底层会话和草稿保留、微信来源本地类型 / 名称筛选和隐藏选择防护。
- 批次开始与结束比较完整前端源及相关5个后端/契约输入共43文件、dist3文件的路径集合/SHA-256/bytes/mtime，harness SHA/bytes亦不变，报告`fingerprints_unchanged=true`；见[指纹](<artifacts/2026-11-18-chat-discovery/source-manifest.json>)与[冻结runner](<artifacts/2026-11-18-chat-discovery/synthetic-browser.mjs>)。
- **最终40项断言、74个拦截API、0 mutation、0真实模型/平台外呼**；console/interception/意外请求/target均0。9次原生鼠标、6次触控、23次键盘；20条模态焦点轨迹中7个不同Agent控件实际获焦，4次BODY且page_focused=false，无底层业务控件获焦，不宣称严格循环。390px是Chromium视口/触控仿真，不是实体手机或原生Tauri验收。

### 最终截图逐一查看

- [桌面群聊关键词](<artifacts/2026-11-18-chat-discovery/desktop-groups-keyword.png>)：两个标签、搜索和求助位于内部滚动区外；正文原文/最近一条/仅名称命中分开，高亮可见，白色扁平界面保留。
- [390px消息发现](<artifacts/2026-11-18-chat-discovery/narrow-groups-keyword.png>)：标签、关键词、搜索/清除及来源跳转可见，控件换行，无文档横向溢出。
- [390px Agent上下文](<artifacts/2026-11-18-chat-discovery/narrow-agent-context.png>)：原草稿与显式追加内容仍在，UTF-8提示和未勾选的模型同意可见，关闭按钮可操作；竖向/文本框内滚动如实保留。
- [390px微信来源](<artifacts/2026-11-18-chat-discovery/narrow-wechat-source.png>)：名称/ID筛选与无匹配提示明确；隐藏peer已清空，读取和监测均禁用。

四图均为合成数据/构建，不代表当前运行实例已更新。[归档说明](<artifacts/2026-11-18-chat-discovery/README.md>)附安全范围、白名单归档及无需运行后台的复跑方式；没有继续视觉微调或扩大浏览器验证批次。

### 失败记录不删除

- 预检 `LCjLSf`：连接按钮定位错误，在任何功能断言前失败；[原报告](<artifacts/2026-11-18-chat-discovery/failures/LCjLSf/report.json>)，未访问实际服务。
- 预检 `4F62UV`：合成状态接口路径错误与自身 target guard 判断错误；0 项功能断言、22 个已拦截 API 请求、0 个 mutation。仅修正 fixture / guard；[原报告](<artifacts/2026-11-18-chat-discovery/failures/4F62UV/report.json>)。
- 预检 `HLJK3m`：1 项断言后，DOM 存在性谓词返回 React 关联节点，CDP `returnByValue` 遇到过长对象引用链；22 个 API、0 mutation。将两个存在性结果显式转 boolean，未改全局严格断言；[原报告](<artifacts/2026-11-18-chat-discovery/failures/HLJK3m/report.json>)。
- 首个功能批次 `ZhV3PR`：13 项断言、34 个 API、0 mutation；窄屏第 3 次 Tab 的 `dialog.contains(activeElement)` 不满足，[原报告](<artifacts/2026-11-18-chat-discovery/failures/ZhV3PR/report.json>)与[当时截图](<artifacts/2026-11-18-chat-discovery/failures/ZhV3PR/failure.png>)保留。此报告未记录具体 activeElement，**不能证明底层业务控件获得焦点**。
- 经[已安装指南 §12](<../../.agents/skills/modern-web-guidance/guides/accessibility/accessibility.md#L540-L548>)核实，原生 modal 允许 Tab 到浏览器 chrome，本不应额外实现 focus trap。中途提出的局部 trap / 第 10 组测试已全部撤回，不属于交付；恢复后的 [App](<../../apps/desktop/src/App.tsx>) SHA-256 `9e2810f0d222213bf6ada43ac69e4616658297cb50c3d8ab114fc7a60ebf6808` 与首个批次的冻结指纹一致。撤回版本的 10 组/tsc 结果不冒充最终版本验证。
- 最终 harness 改为检查真正的原生模态语义：`:modal`、对底页控件的程序性 focus 被拒、18 次真实 Tab 和首/尾 Tab / Shift+Tab 只允许模态内容或文档容器，从不允许底层业务控件获得焦点；逐项记录 page focus、节点 tag/id/index，关闭的恢复表单不可聚焦，再以真实触控返回草稿并验证 Esc 回返。浏览器 chrome 的可能参与与容器焦点如实记录，不宣称严格循环或把容器直接当成 chrome。
- 上述失败日志、报告、截图和指纹与最终唯一确认轮分开归档；不把失败重写为成功，不进行无界截图 / 微调 / 重建循环。

## 4. 未完成项与交付边界

- 待 joey 正式审后端、angjustinl 正式审前端，非作者正式审脚本；kirinbao 在明确授权更新运行版本后验收真实微信私聊 / 群聊、历史关键词、模型求助、原生 Tauri 和连接恢复。模拟数据不能代替真实账号授权、隐私和独立验收。
- T08 的 SSE / 断点重连与全任务指标、T10 的 30 条 Recall@5 / 语义召回评测均未由此次字面搜索替代；实际完成日期 **—**，MVP **0/27**、扩展 **0/5** 不变。
- QQ 仅 Milky 1.3；通用自动发送保持关闭。飞书本人早晚报仍是单独的默认关闭窄授权能力，不因 Agent 求助开启计划。平台接受不等于手机通知或已读；不承诺休眠 / 关机后运行。
- 仅检查、提交并推送当前功能分支，不建 PR、不更新 main、不部署、不重启或替换现有后台 / 客户端，不录入实际凭据或调用真实模型 / 平台。**当前运行实例不会因这次源代码提交自动出现新控件。**
