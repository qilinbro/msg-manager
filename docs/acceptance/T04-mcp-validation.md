# T04 MCP 服务验证与待验收记录

- 任务：`T04` / `FEAT-T04`；变更：`CHG-T04-005`
- 工程验证日期：2026-11-17（Asia/Shanghai）；实际完成日期：—
- 交付分支：`feat/mcp-service`；工程实现与检查通过，待正式审查、真实客户端及本人数据验收
- 后端负责人：angjustinl；指定后端审查人：joey（实际正式审查记录：—）
- 本轮未新增前端或桌面模块；仅附带既有微信面板段落的 Prettier 换行对齐，无功能变更；T04 既有前端由 angjustinl 审查非本人模块的要求不变
- 产品、测试与交付验收：kirinbao（实际真实客户端/微信验收记录：—）

## 本轮实现与工程检查

实现采用官方 `github.com/modelcontextprotocol/go-sdk v1.8.0`，不是自维护 JSON-RPC 传输；后台路由、参数与结果契约仍由桥接代码显式限制。自动测试仅使用合成令牌、临时加密数据库和 loopback 测试服务，未读取真实配置、凭据、微信库或聊天，未调用真实模型/账号。

| 检查项 | 2026-11-17 结果 | 可追溯证据 |
|---|---|---|
| URL、token、固定 HTTP 路由、查询/正文/鉴权转发 | 通过 | [server_test.go](../../internal/mcp/server_test.go) |
| 远程地址、URL 凭据/路径/查询/片段（含空 `#`）、环境代理、重定向与响应格式/大小/错误正文边界 | 通过 | [server_test.go](../../internal/mcp/server_test.go) |
| 官方 SDK 握手、ping、工具发现、六工具调用和副作用 annotations | 通过 | [transport_test.go](../../internal/mcp/transport_test.go) |
| 重复参数键、null/错误类型、未知字段/工具、非法 ID，非法请求不触达 HTTP | 通过 | [transport_test.go](../../internal/mcp/transport_test.go)、[main_test.go](../../cmd/sociald-mcp/main_test.go) |
| 输入帧上限、MCP 请求取消、服务运行上下文取消传播到 HTTP | 通过 | [transport_test.go](../../internal/mcp/transport_test.go) |
| 真实命令入口、OS stdio 管道、显式 2025-11-25 initialize、六工具完整 JSON 转发 | 通过 | [main_test.go](../../cmd/sociald-mcp/main_test.go) |
| 跨进程请求取消、401 安全错误、配置失败退出码 2、stdout 无日志/PASS、干净 EOF 退出码 0 | 通过；命令测试乱序重复 10 轮通过 | [main_test.go](../../cmd/sociald-mcp/main_test.go) |
| 不依赖 CGO 的独立桥接二进制构建与实际产物 EOF 冒烟 | 通过；退出码 0，stdout/stderr 均为空 | 下列 `CGO_ENABLED=0` 构建及合成环境启动记录 |
| 实际 HTTP 后台 + 临时 SQLCipher 存储 + MCP 子进程：搜索限域及稳定游标、消息分页、未读不变、空日报、未配置微信 503、错误 token 401 | 通过 | [mcp_test.go](../../tests/integration/mcp_test.go) |
| 完整 Go 格式/测试/vet、API 契约、PowerShell 合成测试、八组客户端回归及前端格式/类型/生产构建 | 通过（脚本退出码 0） | [check.ps1](../../scripts/check.ps1) |
| MCP、命令入口与整个 integration 包的 race 检查；Go 依赖完整性 | 通过；`all modules verified` | 下列实际执行命令、上述四组测试及 [go.sum](../../go.sum) |
| joey 指定非作者审查 | **待执行** | AI 辅助复核不替代正式审查 |
| kirinbao 真实客户端、本人微信/群聊与数据处理验收 | **待执行** | 下方人工清单 |
| 原生 Tauri 构建/打包与 T04 整项验收 | **未完成** | 本轮未运行 `-IncludeDesktop`，不以 Web 构建替代 |

验证环境：Windows amd64，Go `1.27.4`（模块要求 `1.26.0`）、Node `v24.12.0`、GCC `14.2.0`（SQLCipher/race）。完整检查中 Node `stripTypeScriptTypes` 有 experimental warning，未导致失败。

根目录与最终发布工作树均运行完整检查并通过；发布工作树另复跑三包 race、全量 Go vet 和依赖完整性校验。发布补检初次因 Windows CRLF 检出导致格式检查失败；规范化换行后已确认没有新增 Git 源码差异，仅另附带[微信面板](../../apps/desktop/src/WechatPanel.tsx#L418-L424)现有段落的 Prettier 换行对齐。未将根目录其他后台功能改动混入本轮，发布快照按实际提交内容单独全检。

实际执行的关键命令（Go 使用项目便携工具链，设置项目缓存、`GOTOOLCHAIN=local`；含 SQLCipher/race 时设置 `CGO_ENABLED=1` 和本机 GCC）：

```powershell
# 配置/协议、命令进程与合成实际后台验证
go test -tags sqlite_fts5 -count=1 -timeout 90s -run '^Test(SDK|ParseArguments|MCPBridge)' ./internal/mcp ./tests/integration
# 桥接独立构建和命令进程回归使用 CGO_ENABLED=0
go build -trimpath -o .cache/mcp-validation/sociald-mcp.exe ./cmd/sociald-mcp
go test -count=1 -timeout 90s -v ./cmd/sociald-mcp
go test -count=10 -shuffle=on -timeout 90s ./cmd/sociald-mcp
# 全工程检查，包含最终 MCP 源码与新测试
.\scripts\check.ps1
# 并发回归与依赖校验（不是漏洞扫描或真实服务验收）
go mod verify
go test -race -tags sqlite_fts5 -count=1 -timeout 120s ./internal/mcp ./cmd/sociald-mcp ./tests/integration
```

独立产物额外用仅含合成 `SOCIALD_URL` / `SOCIALD_API_TOKEN` 的环境启动，立即关闭 stdin；10 秒内干净退出，退出码 0，stdout/stderr 均为空。此冒烟未执行工具调用，不代表任何真实客户端或后台数据已验收。

## 辅助复核与已修复问题

本轮 AI 只读辅助复核检查了路由、鉴权、参数、SDK 生命周期、微信导入/自动整理/日报链路和测试；未充当 joey、angjustinl 或 kirinbao 的正式审批。

1. **微信导入副作用**：修复 `read_wechat_messages` 原有的 `OpenWorldHint=false` 为 `true`，保持 `ReadOnlyHint=false`。描述和服务 Instructions 明确：源微信库只读，但产品库写入联系人、消息与未读；已有授权的自动整理/日报可能把新消息发送给配置模型，可能外部传输并产生费用。其他五个工具仍为只读、本地操作。工具标注有回归断言。
2. **运行上下文取消**：SDK stdio graceful close 默认等待在途工具，单纯取消 `Run` 不会立即取消其 HTTP 请求。新增 `TestSDKServerLifetimeCancellationStopsBackendRequest`，修复前实测失败（3 秒内未取消 HTTP）；将 lifetime 与各工具派生 context 关联后通过，并保持原 MCP 请求取消和 EOF 语义。
3. **如实披露作用域**：本机 bearer token 授权整个本地工作区，不是联系人/工具 allowlist；空搜索和整份日报可能返回多个会话信息。只在 Instructions 里要求“用户选定会话”不能冒充服务端硬隔离。[接入说明](../mcp-setup.md)与 [ADR 0012](../adr/0012-mcp-local-bridge.md)已写明。

未见本轮代码可证实的远程 URL/代理/重定向令牌泄漏或跨工作区读取缺陷；这不是完整漏洞扫描或所有客户端兼容性保证。

## 保留边界

- MCP 客户端可能把工具结果交给它自己的外部模型；即使是只读工具，也必须由用户确认客户端数据策略。导入还可能触发**后台既有授权**的模型处理，不是新开 AI 授权，也不能宣传“绝不联网”。
- 微信关键词仅筛选最近默认 100 / 最大 500 条源记录窗口，不是全历史检索；超大微信会话列表目前没有分页，1 MiB 响应上限会明确报错，改用工作台而非静默截断。
- 请求取消、EOF 或运行上下文取消可中止桥 HTTP；已提交导入不回滚，不自动重试。既有会话/消息 GET 的后端锁等待与 SQL 尚未完整采用 context，不能据合成 HTTP 取消测试声称所有数据库查询即时停止。
- 不开放发送、审批、密钥/路径访问、任意 URL/SQL、监控或模型设置；自动发送仍关闭。桥接不负责唤醒机器，休眠/关机期间不会继续工作。

## 真实验收清单（未执行）

1. kirinbao 选择可信 MCP 客户端，核对其模型、数据保留与外传策略；依据[接入说明](../mcp-setup.md)构建程序并在仓库外安全注入 token，不在截图/聊天/提交中暴露实际值。确认后台端口属于可信本地服务。
2. 完成初始化、ping 和工具发现，确认恰为六个工具；五个查询只读，微信导入为非只读/开放世界提示。记录客户端名称/版本、日期和脱敏结果。
3. 在明确授权的会话范围内测试状态、带平台/关键词的会话筛选、消息分页与最近日报；确认未读数不因查询改变，并注意日报是整份工作区报告，而非单联系人报告。
4. 在产品内完成本人微信授权配置，确认会话工具仅显示该配置账号的私聊/群聊元数据，不返回微信路径、数据库密钥或 DPAPI 内容。
5. 调用导入前核对产品内自动整理/日报的既有授权、启停和费用设置；若不希望新数据进入模型，先暂停相关处理。选择本人有权处理的测试私聊/群聊 ID，分别验证空关键词、中文命中和无命中、重复导入去重，以及产品库保存和未读变化。核实后端以只读方式打开微信源库、不发送聊天消息，不将 live 微信本身的写入误算为 MCP 写源库。
6. 尝试非法参数、路径/SQL/密钥/远程 URL，确认拒绝；客户端界面不得显示不存在的发送、审批、配置工具。错误 token 应只返回安全的鉴权失败摘要，不泄漏服务错误正文。
7. 验证客户端取消和正常断开。若导入途中取消/断开或超时，先核对消息中心是否已经保存，再决定是否重试，不把错误状态等同于“肯定没有写入”。
8. joey 登记后端正式审查结论；kirinbao 登记真实验收、已知限制与最终交付结果。证据须脱敏，不保存真实聊天正文或凭据。指定非作者审查/验收未通过前不填写实际完成日期。

## 当前结论

`CHG-T04-005` 的工程实现、完整检查和合成进程/真实后台联调通过；正式非作者审查、真实 MCP 客户端及本人微信读取尚未完成。T04 整项保持“进行中”，实际完成日期为 —；已验收统计仍为 MVP `0/27`、扩展 `0/5`。分支提交/推送仅表示工程代码交付，不代表功能验收完成，不创建 PR。
