# T04 MCP 服务验证与待验收记录

- 任务：`T04` / `FEAT-T04`；当前变更：`CHG-T04-006`，关联 `CHG-T08-006`；保留 `CHG-T04-005` 历史证据
- 工程验证记录日期：2026-11-17（Asia/Shanghai）；实际完成日期：—
- 交付分支：`feat/mcp-service`；本轮持久连接实现与最终工程检查通过，子项待审查 / 待验收，T04/T08 整项仍进行中；不建 PR、不更新 main
- 后端负责人：angjustinl；指定后端/脚本审查人：joey（实际正式审查记录：—）
- 本轮关联工作台受信任设备前端；前端指定审查人：angjustinl，仅审非本人模块（实际正式审查记录：—）。历史 `CHG-T04-005` 未新增前端功能，仅附带既有微信面板的 Prettier 换行对齐
- 产品、测试与交付验收：kirinbao（实际真实客户端/微信/持久连接验收记录：—）

## CHG-T04-006 当前实现与验证进度

本轮默认链路为 Windows 启动器创建/复用当前用户 DPAPI 服务凭据，MCP 在未显式设置 `SOCIALD_API_TOKEN` 时只读加载；显式环境变量存在即优先，空值或非法值报错，不能回退。默认位置为 Windows **KnownFolder LocalAppData** 下的 `SocialIntelligenceOS\credentials\service-v1.dpapi`，不信任 `LOCALAPPDATA` 环境变量；`SOCIALD_CREDENTIAL_FILE` 仅作本机受保护绝对路径覆写。非 Windows 仍为显式 env-only。

启动器默认不改剪贴板、正常启动复用稳定 token；仅 `-CopyApiToken` 显式复制。`-RotateApiToken` 必须先由操作人正常停止后台，端口仍占用即拒绝，不强杀进程；任意显式 env token（包括空值）均拒绝轮换。已有密文损坏、权限不安全或 URL 不匹配必须失败，不修复、不重建、不降级到其他凭据。普通重启无需重新复制；轮换后需重启 MCP，显式 env 客户端更新令牌并重连，浏览器重新配对。操作见[接入说明](../mcp-setup.md)，决策见 [ADR 0012](../adr/0012-mcp-local-bridge.md) / [ADR 0013](../adr/0013-persistent-local-connections.md)。

| 检查项 | 当前可确认结果 | 证据与尚未覆盖项 |
|---|---|---|
| 服务凭据记录/路径、Windows 只读 DPAPI/ACL/URL 绑定、MCP 显式 env 优先及命令入口回归 | PASS，最终 CGO0 三包分别 7.274s / 0.263s / 4.446s | [凭据测试](<../../internal/localcredentials/credentials_test.go>)、[Windows 测试](<../../internal/localcredentials/credentials_windows_test.go>)、[MCP 测试](<../../internal/mcp/server_test.go>)、[命令测试](<../../cmd/sociald-mcp/main_test.go>)；真实原生 Go 只读 DPAPI，无 PowerShell 运行时读取依赖 |
| 启动器稳定复用、默认不复制、显式复制/轮换、端口占用与 env 模式拒绝、坏文件与权限不变 | PASS，最终全检 53 次隔离启动 | 41 次真实 core / 合成环境 + 12 次真实 PS5.1 wrapper / 惰性 core；[合成启动器测试](<../../scripts/test-inbox-launcher.ps1>)。含混合大小写 env 存在性、空值拒绝与 null 环境清理；不是真实后台重启验收 |
| PowerShell writer → Go / Go fixture → PowerShell 读取临时 DPAPI 记录、坏文件不改写 | PASS，含上述凭据包与最终 race | [互操作测试](<../../internal/localcredentials/powershell_windows_test.go>)；仅临时合成凭据，不读取真实默认文件。真正第二个 Windows 用户仍未验收 |
| 受保护 saved-token 经多个实际 MCP stdio 进程连接临时 SQLCipher HTTP 后台 | PASS，实施者 integration 专项 2.525s / race 8.882s；最终扩大 race 再覆盖 | [凭据集成测试](<../../tests/integration/mcp_credentials_windows_test.go>)；临时库重开、受保护字节不变及显式 env 优先，不等于实际 sociald 进程重启或真实客户端验收 |
| 可撤销设备授权的 v12 迁移、加密存储、重启/撤销、过期/issuer/service 物理清理、32 台上限及并发/取消 | PASS | [存储测试](<../../internal/store/local_devices_test.go>)；全 store 15.323s，设备 race 三轮 15.116s；最终扩大 race 再覆盖 |
| 9 组 `TestLocalDevice*` API 与监听端口占用不改 grant | PASS，API 定向 race 两轮 5.545s；修复后 cmd/API race 及扩大 race 通过 | 实际 listener IP/端口 A→B→A、AI 锁旁路；[启动回归](<../../cmd/sociald/main_test.go>) 验证 bind-before-prune 和构造失败释放端口 |
| 工作台配对/恢复/忘记/撤销与有界浏览器检查 | PASS，设备 31 组 / 内存会话 11 组 / 7 个业务脚本，浏览器 6 项 | [设备验证](<T08-persistent-device-validation.md>)；隔离 Chromium 最终构建/真实 WebCrypto+IDB+合成 HTTP，桌面与窄屏一次确认；不等于真实跨端验收 |
| 本轮最终完整工程检查 / mod verify / 扩大 race / CGO0 构建与 EOF | PASS | 完整检查 38.34s，2026-11-17 18:34:23 +08:00 结束，退出 0；mod 全部验证，扩大 race 非缓存；桥接 EOF 退出 0、协议 stdout/stderr 均空且无后台请求；命令如下 |
| joey 后端/脚本指定非作者审查 | **待执行** | 实际正式审查记录：—；AI 辅助复核不替代审批 |
| angjustinl 关联前端指定非作者审查 | **待执行** | 实际正式审查记录：—；不得同人自审 |
| kirinbao 真实 Windows 重启、客户端、浏览器及本人微信验收 | **待执行** | 下方人工清单；真正第二 Windows 用户的 DPAPI 隔离仍须真实验证 |

已实际执行的本轮专项命令（合成数据，无真实凭据或本人数据）：

```powershell
# 整合者最终 Windows 专项，CGO_ENABLED=0；构建输出仅在忽略的临时目录
$env:CGO_ENABLED = '0'
go test ./internal/localcredentials/... ./internal/mcp ./cmd/sociald-mcp -count=1
go build -o .runtime/persistent-validation/sociald-mcp.exe ./cmd/sociald-mcp
# 另以空 stdin 执行该桥接进行 EOF / 零 HTTP 冒烟，未输出或使用真实凭据

# 存储实施者临时 SQLCipher 专项；已配置本机 GCC
$env:CGO_ENABLED = '1'
go test -tags sqlite_fts5 ./internal/store -count=1
go test -race -tags sqlite_fts5 ./internal/store -run TestLocalDevices -count=3
# 整合者 API 定向与修复后扩大回归
go test -race -tags sqlite_fts5 ./internal/api -run '^TestLocalDevice' -count=2 -timeout 120s
go mod verify
go test -race -tags sqlite_fts5 ./cmd/sociald ./cmd/sociald-mcp ./internal/api ./internal/localcredentials/... ./internal/mcp ./internal/store ./tests/integration -count=1 -timeout 240s
# 最终全检包含 53 次隔离启动、bootstrap 9 子用例及全部前端检查
.\scripts\check.ps1
```

最终完整检查、扩大 race、CGO0 三包/构建/EOF 与隔离浏览器由整合者实际执行并收集退出码；53 次启动由最终全检再次执行，DPAPI/saved-token 专项由 MCP 实施者执行，store 定向专项由存储实施者执行。扩大 race 各包结果：cmd/sociald 3.792s、cmd/sociald-mcp 13.077s、API 16.251s、localcredentials 10.111s、MCP 3.906s、store 21.663s、integration 11.512s，全部退出 0。

普通只读辅助复核发现的 3 项 P2（重复实例先 prune 后 bind、短 proof 401 误删长期密钥、hook 重配失败误删旧 descriptor）均已定点修复，并经过上述最终回归；另补唯一业务 read retry 401 不删长期密钥的保护。详细闭环见 [设备验证](<T08-persistent-device-validation.md>)。AI 辅助复核不是 joey/angjustinl 正式审批，合成后台/浏览器不是 kirinbao 的真实客户端、本人数据或原生 Tauri 验收。DPAPI/ACL 只保护落盘，loopback HTTP 无服务器身份或多 OS 用户隔离，应仅用于可信个人机；未重启或接管现有服务，不预填新提交哈希。

## CHG-T04-005 历史实现与工程检查

以下保留该变更当时的证据；其中“本轮”“完整检查通过”仅指 `CHG-T04-005`，不能推导 `CHG-T04-006` 已通过。

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

## CHG-T04-005 历史辅助复核与已修复问题

以下为 `CHG-T04-005` 当时的 AI 只读辅助复核，检查了路由、鉴权、参数、SDK 生命周期、微信导入/自动整理/日报链路和测试；未充当 joey、angjustinl 或 kirinbao 的正式审批，也不构成本轮持久连接的指定审查。

1. **微信导入副作用**：修复 `read_wechat_messages` 原有的 `OpenWorldHint=false` 为 `true`，保持 `ReadOnlyHint=false`。描述和服务 Instructions 明确：源微信库只读，但产品库写入联系人、消息与未读；已有授权的自动整理/日报可能把新消息发送给配置模型，可能外部传输并产生费用。其他五个工具仍为只读、本地操作。工具标注有回归断言。
2. **运行上下文取消**：SDK stdio graceful close 默认等待在途工具，单纯取消 `Run` 不会立即取消其 HTTP 请求。新增 `TestSDKServerLifetimeCancellationStopsBackendRequest`，修复前实测失败（3 秒内未取消 HTTP）；将 lifetime 与各工具派生 context 关联后通过，并保持原 MCP 请求取消和 EOF 语义。
3. **如实披露作用域**：本机 bearer token 授权整个本地工作区，不是联系人/工具 allowlist；空搜索和整份日报可能返回多个会话信息。只在 Instructions 里要求“用户选定会话”不能冒充服务端硬隔离。[接入说明](../mcp-setup.md)与 [ADR 0012](../adr/0012-mcp-local-bridge.md)已写明。

未见本轮代码可证实的远程 URL/代理/重定向令牌泄漏或跨工作区读取缺陷；这不是完整漏洞扫描或所有客户端兼容性保证。

## 保留边界

- MCP 客户端可能把工具结果交给它自己的外部模型；即使是只读工具，也必须由用户确认客户端数据策略。导入还可能触发**后台既有授权**的模型处理，不是新开 AI 授权，也不能宣传“绝不联网”。
- 微信关键词仅筛选最近默认 100 / 最大 500 条源记录窗口，不是全历史检索；超大微信会话列表目前没有分页，1 MiB 响应上限会明确报错，改用工作台而非静默截断。
- 请求取消、EOF 或运行上下文取消可中止桥 HTTP；已提交导入不回滚，不自动重试。既有会话/消息 GET 的后端锁等待与 SQL 尚未完整采用 context，不能据合成 HTTP 取消测试声称所有数据库查询即时停止。
- 不开放发送、审批、通过工具访问密钥/路径、任意 URL/SQL、监控或模型设置；自动发送仍关闭。服务 DPAPI 只用于桥接鉴权，不是微信配置入口，MCP 不直接打开产品或微信数据库、不读取微信/产品数据库密钥。
- DPAPI CurrentUser 和私有 ACL 不抵御同一用户的恶意进程，也不保证管理员/系统完全控制下的隔离；工作区级 token 不能冒充逐客户端/联系人权限。真正第二 Windows 用户隔离尚待独立验证。
- 持久凭据不新增后台驻留、远程服务、唤醒机器或离线执行保障；后台停止、休眠/关机期间不会继续工作，也不证明平台持续在线。外部模型、客户端网络和数据策略仍由各自配置约束。

## 真实验收清单（未执行）

1. kirinbao 选择可信 MCP 客户端，核对其模型、数据保留与外传策略；依据[接入说明](../mcp-setup.md)构建程序。Windows 默认用同一用户和仅含 `command` 的配置，确认客户端及继承环境没有显式 token；需恢复默认时用 `Remove-Item Env:SOCIALD_API_TOKEN` 真正 unset，不赋空。确认后台端口属于可信本地服务；不在截图/聊天/提交中暴露实际凭据。
2. 完成初始化、ping 和工具发现，确认恰为六个工具；五个查询只读，微信导入为非只读/开放世界提示。记录客户端名称/版本、日期和脱敏结果。
3. 在明确授权的会话范围内测试状态、带平台/关键词的会话筛选、消息分页与最近日报；确认未读数不因查询改变，并注意日报是整份工作区报告，而非单联系人报告。
4. 在产品内完成本人微信授权配置，确认会话工具仅显示该配置账号的私聊/群聊元数据，不返回微信路径、数据库密钥或 DPAPI 内容；桥接只加载服务凭据，不直接接触微信配置密钥或数据库。
5. 调用导入前核对产品内自动整理/日报的既有授权、启停和费用设置；若不希望新数据进入模型，先暂停相关处理。选择本人有权处理的测试私聊/群聊 ID，分别验证空关键词、中文命中和无命中、重复导入去重，以及产品库保存和未读变化。核实后端以只读方式打开微信源库、不发送聊天消息，不将 live 微信本身的写入误算为 MCP 写源库。
6. 尝试非法参数、路径/SQL/密钥/远程 URL，确认拒绝；客户端界面不得显示不存在的发送、审批、配置工具。错误 token 应只返回安全的鉴权失败摘要，不泄漏服务错误正文。
7. 验证客户端取消和正常断开。若导入途中取消/断开或超时，先核对消息中心是否已经保存，再决定是否重试，不把错误状态等同于“肯定没有写入”。
8. 默认启动后台，确认未复制/打印 token；正常停止并重启后台与 MCP，确认凭据稳定、无需重复复制。验证浏览器关闭再打开后有效设备授权可恢复；清除站点数据、过期或忘记/撤销后须重新配对。后台停止、休眠/关机时不得显示为仍在后台持续处理。
9. 需首次/重新授权浏览器时才在启动时显式 `-CopyApiToken`，确认剪贴板风险提示。先正常停止后台后显式 `-RotateApiToken`，确认旧 MCP token 失效、重启 MCP 后使用新凭据、env 客户端需安全更新并重连、浏览器旧设备授权失效且重新配对；验证正常重启不等于轮换。
10. 仅用隔离的临时合成记录验证：显式 env 合法值优先且不读文件，空/非法值报错不回退；轮换在后台端口占用或任意显式 env token 下拒绝。MCP 缺失服务凭据失败且不创建，启动器仅在缺失时初始化；已有损坏、ACL 不安全或 URL 不匹配均失败，不修复、不降级。不要为测试故意损坏真实凭据或 ACL，也不删除真实密文来绕过检查。
11. 若验证第二 Windows 用户的真实 DPAPI 隔离，须使用合成记录及独立受控账号，不向其他账号复制真实服务 token；记录未覆盖限制。非 Windows 只验受控 env 模式，不声称可读取 Windows 受保护文件；同用户恶意进程防护不在当前保证内。
12. joey 登记后端/脚本、angjustinl 登记关联前端的非作者正式审查结论；kirinbao 登记真实验收、已知限制与最终交付结果。证据须脱敏，不保存真实聊天正文或凭据。两条指定非作者审查及验收未通过前不填写实际完成日期。

## 当前结论

`CHG-T04-005` 的工程实现、完整检查和合成进程/真实后台联调通过，保留为历史证据。`CHG-T04-006` 的凭据/MCP、启动器/双向互操作、store 与定向 API 已有上述合成专项通过结果；saved-token 临时后台集成、最终完整检查与跨端整合仍待补录，不能据此宣称整项通过。

joey 后端/脚本与 angjustinl 关联前端的两条正式非作者审查、kirinbao 真实 Windows 重启/MCP 客户端/浏览器及本人微信验收均未完成。T04 整项保持“进行中”，实际完成日期为 —；已验收统计仍为 MVP `0/27`、扩展 `0/5`。分支提交/推送仅表示工程代码交付，不代表功能验收完成，不创建 PR。
