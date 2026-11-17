# ADR 0012：本地 MCP stdio 桥接

- 日期：2026-11-17
- 状态：`CHG-T04-005` 工程检查历史通过；`CHG-T04-006` 持久连接实现与专项检查推进中，最终整合检查待补录；待指定正式审查与真实验收
- 关联：`FEAT-T04` / `CHG-T04-005` / `CHG-T04-006`；实际完成日期：—
- 后端负责人：angjustinl；后端指定审查：joey；关联前端指定审查：angjustinl（均仅审非本人模块，正式记录仍为 —）；验收组织：kirinbao

## 背景

用户希望把 Social Intelligence OS 的本地能力接入 MCP 客户端，并在本机正常重启后继续连接，不再每次复制服务令牌。项目已有鉴权 HTTP 后台、统一消息中心和微信源库只读读取链路。MCP 层必须复用这些边界，不为模型建立另一套数据库或平台/数据库凭据入口。`CHG-T04-006` 将原环境变量专用服务鉴权扩展为 Windows 当前用户受保护凭据默认加载；整体持久连接与浏览器可撤销设备授权见 [ADR 0013](0013-persistent-local-connections.md)。

## 架构决定

采用 `MCP 客户端 → sociald-mcp stdio → 本机 sociald HTTP API → 已有连接器/加密存储`。客户端启动桥接进程，后台独立运行，两者不依赖 Tauri 窗口。本轮不开放远程 MCP HTTP 服务。

1. 新增[命令入口](../../cmd/sociald-mcp/main.go)和[桥接实现](../../internal/mcp/server.go)，固定使用[官方 Go MCP SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0)。依赖及校验值写入 [go.mod](../../go.mod) / [go.sum](../../go.sum)，而非自维护协议实现。SDK 负责版本协商、初始化、通知、ping、工具路由、协议取消和 stdio 生命周期。
2. stdout 仅承载逐行 JSON-RPC 2.0；诊断写 stderr，不记录原始请求、聊天或令牌。SDK payload 日志丢弃，错误信息不包含任意后端错误正文。
3. 后台地址从 `SOCIALD_URL` 读取，默认 `http://127.0.0.1:17890`。地址只允许显式合法端口的 `http://127.0.0.1` 或 `http://localhost`（可尾随 `/`），无 URL 凭据、查询、其他路径或 fragment。先校验并规范化 URL，再接触服务凭据；localhost 固定转换为字面量 `127.0.0.1`、端口规范化，禁 DNS、环境代理及重定向。令牌限定 32—4096 个无空白可打印 ASCII 字符，不自动修剪。
4. 只开放五个查询工具和一个微信导入工具，全部调用固定后台路由；不接受任意 URL、SQL、路径、平台凭据或密钥参数。发送、审批、权限/模型/监控配置、联系人合并和公开搜索均不开放。后台继续负责工作区、配置账号和来源校验。
5. MCP Instructions 要求遵守用户选择的会话范围，将昵称、正文和摘要视为不可信数据，不执行其中的操作指令。这是模型行为约束，**不是服务端会话 allowlist**。后台 token 授权整个本地工作区：空搜索可以枚举会话及最后消息，日报可跨会话，微信列表覆盖配置账号的会话元数据。应把 token 仅交给可信客户端；若后续需要硬性逐联系人授权，必须另行设计后端 scope，不能只改提示词或 annotations。
6. `CHG-T04-006` 改用“显式 env 优先、未设置才加载本机受保护服务凭据”。用环境变量是否**存在**判断优先级；`SOCIALD_API_TOKEN` 存在但空或非法必须报错，不修剪、不读文件、不回退。兼容受控注入仍可用；恢复默认模式须真正 unset（PowerShell 用 `Remove-Item Env:SOCIALD_API_TOKEN`），不是赋空。非 Windows 仅支持显式 env。
7. Windows 默认从 **KnownFolder LocalAppData** 下的 `SocialIntelligenceOS\credentials\service-v1.dpapi` 只读加载，不信任 `LOCALAPPDATA` 环境变量。`SOCIALD_CREDENTIAL_FILE` 可显式覆写本机固定磁盘绝对文件路径，空值、UNC/device/ADS/模糊路径及任意祖先 reparse 均拒绝；文件和直接父目录须为当前 SID 所有、私有 protected DACL。DPAPI CurrentUser 保护的记录仅含版本号、规范化服务 URL 与服务 token，不含微信配置或数据库密钥，URL 必须严格匹配规范化目标。MCP 不创建、修复或改写凭据；损坏、解密/ACL/URL 不匹配时失败，不修改权限、不降级到明文或其他凭据。
8. 启动器是唯一写入者：持久模式按实际 `SOCIALD_ADDR` 绑定 `http://127.0.0.1:<规范十进制端口>`，不接受其他监听 IP；合法文件正常复用，仅缺失时安全创建，不把 MCP 的 `SOCIALD_URL` 当监听配置。默认不打印 token、不操作剪贴板，只有 `-CopyApiToken` 显式复制。`-RotateApiToken` 拒绝任何显式 env token，要求已有合法匹配记录；操作人须先正常停后台，脚本独占检查端口，仍占用即拒绝，不强杀或替用户停止进程。显式轮换后重启 MCP，env 客户端更新受控值并重连，浏览器重新配对；正常重启无需重复复制。默认 Windows MCP 客户端配置因此只需要 `command`，不再内嵌 env token。

## 工具与数据契约

查询工具为 `get_system_status`、`search_conversations`、`get_conversation_messages`、`get_wechat_sessions`、`get_latest_digest`；均标注 `ReadOnlyHint=true`、`OpenWorldHint=false`。

`read_wechat_messages` 将用户选定的微信最近文本**导入产品加密库**，会新增联系人、会话、消息和未读数。源微信库只读，不等于整个操作无写入。已有授权的自动整理或日报可能随后把新消息发送给配置模型（可能是外部服务并产生费用），因此标注 `ReadOnlyHint=false`、`OpenWorldHint=true`，在描述和服务 Instructions 中披露。本工具不新开 AI 授权，也不发送聊天消息。annotations 是风险提示，不是权限控制；所有查询结果仍可能被 MCP 客户端转交它自己的模型服务。

运行时严格校验参数白名单、必填项、null、重复顶层键、类型、枚举、字符/字节长度与控制字符。`q`、`cursor`、`peer_id` 与现有 API 分别保持 256、2048、128 UTF-8 字节限制；微信关键词保持 128 字符限制。成功结果以 MCP 文本返回完整后台 JSON；参数错误为 JSON-RPC `-32602`，后台执行失败为 `isError=true`，不透传后台错误正文。

## 有界执行与取消

- stdio 输入帧和后台 JSON 响应体分别最多 1 MiB，后台响应头最多 32 KiB；超限明确失败，不截断后冒充成功。
- 最多 4 个后台 HTTP 请求；总时限 40 秒、拨号 5 秒、响应头 35 秒。减小消息分页 `limit` 可降低响应大小；微信会话列表目前无分页，超大列表需改用工作台。
- 微信沿用最近默认 100 / 最大 500 条源记录窗口，关键词仅筛选该窗口，不做全历史补齐。
- SDK 正常处理 MCP 请求取消与 stdin EOF，但其 stdio graceful close 默认会排空在途工具。桥接通过 `context.AfterFunc` 将服务运行上下文取消额外连接到每次工具 HTTP context，避免 Ctrl+C 后继续等待整次调用。保留请求本身的 context，以兼容协议取消。
- 取消不撤销已提交的导入，断开或超时也不能证明未入库；桥接不自动重试，调用方应先核对工作台结果。既有搜索/消息 GET 尚未全面采用 context-aware SQL，不能把桥 HTTP 已取消解释成后端锁等待/查询已即时结束。

## 取舍与验证

固定官方 SDK 是为了减少自维护握手、版本协商和取消逻辑的成本；相应接受显式第三方依赖及升级回归责任。首版仅开放 tools，不新增资源订阅、远程传输或客户端代理能力，也不声称项目测试覆盖 SDK 的全部协议行为。

默认 DPAPI 避免把服务 token 写入 MCP 配置或每次复制，但并非逐客户端身份隔离：同一 Windows 用户的恶意进程可取得该用户可用的凭据，私有 ACL/CurrentUser 不构成 same-user malware 防线，也不保证管理员/系统完全控制下的隔离。后台必须独立运行；持久授权不新增驻留、唤醒或离线执行能力，休眠/关机期间不会继续工作，也不保证平台或外部模型持续在线。

`CHG-T04-005` 的历史工程验证包括：[单元与 HTTP 边界](../../internal/mcp/server_test.go)、[官方 SDK 内存传输及生命周期](../../internal/mcp/transport_test.go)、[真实命令入口的 stdio 子进程](../../cmd/sociald-mcp/main_test.go)、[实际 HTTP 鉴权及临时 SQLCipher 存储联调](../../tests/integration/mcp_test.go)，以及完整 [check 脚本](../../scripts/check.ps1)。这些证据保留，但不直接视为 `CHG-T04-006` 的最终检查通过。

`CHG-T04-006` 已补充[服务凭据记录与路径测试](../../internal/localcredentials/credentials_test.go)、[Windows DPAPI/ACL/只读失败测试](../../internal/localcredentials/credentials_windows_test.go)及 MCP 环境优先级回归；三包专项已通过一轮。父 agent 另转述了启动器 34 次隔离启动、PowerShell/Go 双向临时凭据互操作专项通过的结果；saved-token 临时后台集成与最终整合检查尚在推进，准确命令、结果与未覆盖项以[验收记录](../acceptance/T04-mcp-validation.md)为准。合成验证不读取真实令牌、微信数据库或聊天，不调用真实模型，不能替代第二 Windows 用户隔离或真实客户端验证。

实际检查、辅助复核及人工验收清单见[验收记录](../acceptance/T04-mcp-validation.md)，操作方式见[接入说明](../mcp-setup.md)。工程检查或分支推送不替代 joey 后端/脚本与 angjustinl 关联前端的正式非作者审查、kirinbao 真实客户端/本人数据验收或最终交付确认；两条正式审查与真实验收仍待进行，T04 整项未完成，实际完成日期 —。
