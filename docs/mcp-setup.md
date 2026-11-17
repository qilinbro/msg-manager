# 本地 MCP 服务

`sociald-mcp` 是独立的 stdio 桥接进程，使用官方 Go MCP SDK（`github.com/modelcontextprotocol/go-sdk v1.8.0`），复用已鉴权的本地 `sociald` HTTP API。Windows 默认仅只读加载当前用户 DPAPI 保护的**服务访问凭据**，所有工具仍只调用后台 API；桥接不打开微信或产品数据库、不扫描微信目录、不读取微信 DPAPI 配置，也不获取微信/产品数据库密钥。

> `CHG-T04-006` 更新默认连接方式；既有 `CHG-T04-005` 检查结果作为历史证据保留。本轮实现及最终检查进度见[验收记录](acceptance/T04-mcp-validation.md)，不代表真实客户端已验收。

> **授权范围**：后台令牌代表整个本地工作区，不是单联系人令牌。空搜索可列出工作区会话和最后消息，微信会话工具列出已配置账号的会话元数据，日报包含跨会话内容。目前没有 MCP 会话 allowlist 或逐工具权限隔离。仅接入你信任的客户端；客户端及其模型可能接收查询结果，是否把结果发送外部模型由客户端设置决定。模型须先取得用户授权并遵守所选会话范围，不能把工具提示当成硬性隔离。

## 启动前提与构建

先按[消息中心说明](unified-inbox.md)启动本地后台；需要微信读取时，先在产品内完成[本机微信配置](wechat-db-setup.md)。不要把路径、微信密钥或完整配置提供给 MCP 客户端。

```powershell
# 默认模式先真正移除旧兼容 env token，而非赋空；已有后台实例先正常停止。
Remove-Item Env:SOCIALD_API_TOKEN -ErrorAction SilentlyContinue
.\scripts\start-inbox.ps1
```

MCP 构建要求 Go 1.26+；构建好的桥接程序运行时不需要 Go、GCC 或直接链接 SQLCipher（后台的 SQLCipher 构建要求不变）。建议预先构建二进制，避免每次客户端连接都执行编译：

```powershell
# 在项目根目录执行；如 Go 不在 PATH，可用已安装的 .\.tools\go\bin\go.exe
New-Item -ItemType Directory -Force -Path .runtime | Out-Null
go build -trimpath -o .runtime/sociald-mcp.exe ./cmd/sociald-mcp
```

### Windows 默认：自动加载服务凭据

以**启动器所属的同一 Windows 用户**启动 MCP 客户端。未设置 `SOCIALD_API_TOKEN` 时，桥接只读加载 Windows KnownFolder **LocalAppData** 下的 `SocialIntelligenceOS\credentials\service-v1.dpapi`；默认路径由系统 API 解析，不取自可改写的 `LOCALAPPDATA` 环境变量。启动器首次创建该文件，之后正常启动复用稳定 token；MCP 自己不创建或改写凭据，也不需要浏览器先配对。

若此前设置过兼容 env token，要恢复默认模式，须从 **MCP 客户端本身及其继承环境**真正移除该变量，不能把值赋为空；重启客户端或桥接进程使新环境生效：

```powershell
Remove-Item Env:SOCIALD_API_TOKEN -ErrorAction SilentlyContinue
# 若需直接观察 stdio 进程，可在上述同一用户终端运行：
& '.\.runtime\sociald-mcp.exe'
# 开发时也可使用：go run ./cmd/sociald-mcp
```

直接运行后等待 stdin 是正常现象；它不是交互式命令行，需要 MCP 客户端通过管道初始化，也没有浏览器页面或 HTTP MCP 地址；后台端口不是远程 MCP endpoint。stdio 使用每行一个 JSON-RPC 2.0 消息；stdout 仅输出协议，诊断仅写 stderr，不打印令牌或后端错误正文。启动时凭据校验失败会退出，不能完成握手；握手成功也不代表后台已可访问，后台连接或鉴权错误会在工具调用时返回。后台需保持运行，本机休眠/关机期间不会继续工作。

## 客户端配置示例

**Windows 默认配置只有 `command`，不填 env token**。以下为通用 `mcpServers` 形态，按本机位置修改可执行文件路径；具体字段（尤其 `cwd`、环境继承和安全存储）以客户端为准。即使配置里没写 token，客户端继承的显式 `SOCIALD_API_TOKEN` 仍优先；空值会失败而非启用默认凭据。

```json
{
  "mcpServers": {
    "social-intelligence-os": {
      "command": "D:/msg_manager/.runtime/sociald-mcp.exe"
    }
  }
}
```

使用绝对可执行文件路径时不依赖工作目录；`go run` 模式则需要客户端把工作目录设为项目根目录并能找到 Go。不要把本例视为任意具体客户端已通过真实验收。

### 兼容选项：受控环境显式注入

非 Windows **仅支持 env 模式**，不会尝试跨平台解密 Windows 文件。Windows 也可为既有客户端使用该兼容方式：由客户端本机安全存储或受控启动环境注入当前服务 token。以下只给占位符，不要将真实值写入仓库、脚本、命令行参数或聊天记录：

```powershell
$env:SOCIALD_URL = 'http://127.0.0.1:17890'
$env:SOCIALD_API_TOKEN = '<由本机安全存储受控注入的服务令牌>'
& '.\.runtime\sociald-mcp.exe'
```

`SOCIALD_API_TOKEN` **只要存在就优先**：必须为 32—4096 个无空白可打印 ASCII 字符；空值、空白或非法值立即报错，不读取受保护文件，也不回退。显式 env 模式不读取或改写已保存的服务凭据，因此后台和客户端必须使用相同的当前 token；不能假设旧文件已被同步更新。若客户端只能在其配置文件里保存 token，该文件应在仓库之外、仅当前用户可读、不进 Git 或云同步；优先使用安全存储，JSON 占位符不会自动展开为环境变量。用完兼容模式后通过 `Remove-Item Env:SOCIALD_API_TOKEN` 真正 unset，而非赋空。

### 服务地址与本机受保护路径覆写

- `SOCIALD_URL` 是 MCP 目标地址，默认 `http://127.0.0.1:17890`。启动器按后台实际监听变量 `SOCIALD_ADDR` 生成凭据绑定，持久模式要求 `127.0.0.1:<规范十进制端口>`；不能仅改 MCP URL 就把 token 转给另一个本地服务。
- 可以设置 `SOCIALD_CREDENTIAL_FILE` 覆写服务凭据文件位置；**启动器与 MCP 必须配置同一路径**。它不是微信配置路径或数据库密钥路径。覆写必须是本机固定磁盘上的绝对文件路径，不能是 UNC、设备路径、ADS、相对/模糊路径，任何祖先都不能是 reparse point。恢复默认路径须显式移除覆写变量（`Remove-Item Env:SOCIALD_CREDENTIAL_FILE -ErrorAction SilentlyContinue`），不能赋空。
- 文件及直接父目录须为当前用户所有，使用仅当前 SID 显式允许完全控制的私有 protected DACL。启动器在合法路径下缺少文件时初始化，所需新目录/文件创建时即附加私有权限；已有文件、已有目录权限不安全时不调整 owner/ACL，不覆盖坏文件。MCP 始终只读，缺失也不创建；凭据模式下显式空覆写报错，不再尝试默认路径。
- 服务记录须为支持的版本、可由当前用户解密且严格匹配规范化服务 URL（含端口）。损坏、过大、解密失败、ACL 不安全或 URL 不匹配都必须失败，**不修复、不静默轮换、不降级到明文文件或其他地址/凭据**。保留原文件，先核对 Windows 账号、原绑定端口和配置；不要删除密文或放宽 ACL 来掩盖错误。

### 正常重启、首次配对与显式轮换

正常重启后台或 MCP 时无需重新复制 token；有效且未撤销的浏览器设备授权可恢复连接。浏览器清空站点数据、授权过期、忘记/撤销设备后仍需重新配对，详见[持久连接决策](adr/0013-persistent-local-connections.md)。

默认启动不操作剪贴板、不打印 token。需要为浏览器首次配对或重新配对时，在**启动后台时**显式使用：

```powershell
# 已有后台实例时，先在原终端正常停止，勿重复启动。
.\scripts\start-inbox.ps1 -CopyApiToken
```

该开关才会复制当前服务 token。只粘贴到可信的本机工作台授权界面；剪贴板管理器可能保留历史，不把 token 发给模型或截图共享。

需要使旧 token 失效时，先在原后台终端正常停止服务。轮换要求已有合法、URL 匹配的受保护记录；它不是修复坏文件或改绑地址的手段：

```powershell
# 先正常停止旧后台；并确认当前进程不处于显式 env token 模式。
Remove-Item Env:SOCIALD_API_TOKEN -ErrorAction SilentlyContinue
.\scripts\start-inbox.ps1 -RotateApiToken
# 如需在此次启动复制新 token 供浏览器重新配对，用以下命令替换上行：
# .\scripts\start-inbox.ps1 -RotateApiToken -CopyApiToken
```

`-RotateApiToken` 遇到任何显式 env token（包括空值）立即拒绝。脚本在写入前独占检查后台 loopback 端口，仍占用就提示先停后台，不按端口强杀进程，也不更改凭据；成功后按启动器正常流程启动服务。轮换后：**重启所有 MCP 桥接进程**以加载新 token；显式 env 客户端须更新受控注入值并重连；**浏览器重新配对**，旧设备授权不会因切回旧令牌而自动恢复。关闭窗口、休眠或关机不等于后台持续在线。

## 六个 MCP 工具

| 工具 | 对应后台 API | 作用与副作用 |
|---|---|---|
| `get_system_status` | `GET /api/v1/system` | 本地服务、存储和连接器能力；只读，不等于微信持续在线 |
| `search_conversations` | `GET /api/v1/conversations` | 已保存会话的关键词、平台、分类、未读、置顶、归档筛选及游标分页；只读，不读取全平台历史 |
| `get_conversation_messages` | `GET /api/v1/conversations/{id}/messages` | 单个已保存会话的消息分页；只读，不标记已读 |
| `get_wechat_sessions` | `GET /api/v1/connections/wechat-db/sessions` | 已配置本机微信账号的私聊/群聊元数据；源库只读，后台可更新连接诊断状态；不返回源目录或密钥 |
| `read_wechat_messages` | `POST /api/v1/connections/wechat-db/read` | 读取选定会话最近文本并**导入产品加密库**；会新增联系人、消息和未读数。源微信库只读，但这不是全局只读操作 |
| `get_latest_digest` | `GET /api/v1/digest/latest` | 读取已有的工作区日报/提醒，不主动生成；尚无日报返回 `{"report":null}` |

读取已保存消息时，将搜索结果的 `items[].id` 传给 `conversation_id` 参数；微信导入使用 `get_wechat_sessions` 返回的 `peer_id`。两者不是同一标识，也不能用联系人 ID 替代。

**微信导入与模型处理**：已有授权的自动整理或日报可能随后读取导入的新消息，并发送给已配置的模型（可能为外部服务、消耗额度或产生费用）。调用前确认这些既有授权和数据范围；MCP 不新增 AI 授权、不开启自动整理/日报，也不提供修改这些设置的工具。若不希望导入数据进入模型，应先在产品内核对并暂停相关自动处理。该工具 `ReadOnlyHint=false`、`OpenWorldHint=true`；其余五个工具为只读、封闭本地操作。标注是客户端提示，不是权限控制，也不保证客户端不会外传结果。所有工具都没有发送聊天消息的能力。

### 参数边界

- `search_conversations`：`q`、`platform`、`category`、`unread`、`pinned`、`archived`、`cursor`、`limit`；平台为 `qq|weixin|telegram|shanliao|email`，分类为 `friend|family|colleague|client|partner|other|unclassified`。关键词上限 256 UTF-8 字节。
- `get_conversation_messages`：必填 `conversation_id`（1—128 个 ASCII 字母、数字、`_` 或 `-`），可选 `cursor`、`limit`。消息分页不支持额外筛选。
- 两个分页工具的 `limit` 默认为 50，范围 1—100；`cursor` 上限 2048 UTF-8 字节，必须原样使用后端返回的游标，不自行拼接。
- `read_wechat_messages`：必填 `peer_id`（最多 128 UTF-8 字节、无首尾空白和路径分隔符），可选 `keyword`（最多 128 字符、无控制字符，去除首尾空白）。群聊使用后端返回的 `@@...` ID；不要凭昵称构造 ID。
- 其余三个工具无参数。所有工具拒绝额外字段、错误类型、显式 `null`、重复顶层参数键和控制字符；无参数可省略 `arguments` 或使用 `{}`。
- 微信读取仍受最近默认 100 / 最大 500 条**源记录窗口**限制；关键词只在该窗口内匹配，不等于完整历史搜索。

## 安全与协议边界

- `SOCIALD_URL` 缺省为 `http://127.0.0.1:17890`，非空时必须是带显式端口（1—65535）的 `http://127.0.0.1` 或 `http://localhost`，允许单个尾随 `/`；拒绝远程/IPv6/HTTPS、URL 凭据、其他路径、查询、fragment 和编码路径。
- `localhost` 被规范为字面量 `127.0.0.1`；连接不使用 DNS、环境代理或重定向，避免 bearer token 离开固定本机目标。**普通 loopback HTTP 不提供服务端身份或 OS 用户隔离**：后台停机时，不可信本机进程（包括其他 OS 用户）可能抢占相同端口，桥接仍会向该端点发送 bearer。应只用于可信个人电脑，先确认所选端口确为可信的本机 `sociald`；禁代理/重定向不解决端口冒充。
- `SOCIALD_API_TOKEN` 必须是 32—4096 个无空白可打印 ASCII 字符；显式进程环境优先，未设置时 Windows 只读加载受保护服务凭据。非 Windows 为显式 env-only，变量存在但空或非法都必须失败。拒绝将 token、微信密钥、路径、SQL 或任意 URL 作为工具参数。
- Windows DPAPI CurrentUser 与私有 ACL **仅保护凭据落盘**，不补足上述 HTTP 身份边界，**不抵御同一 Windows 用户下的恶意进程**，也不提供抵御管理员/系统完全控制的保证。可信 MCP 客户端可在该用户权限下使用工作区级 token，这不是逐客户端或逐联系人授权。持久连接不新增后台驻留或唤醒能力，也不保证平台持续在线；外部客户端/模型仍受自身网络与数据策略约束。
- 不开放发送、审批、监控配置、模型配置、公开搜索、任意 HTTP、文件或 SQL 工具；微信仍由后端配置账号和源库规则限域。
- SDK 负责版本协商、初始化、通知、ping、取消和 stdio 关闭；仅发布 tools 能力。参数错误为 JSON-RPC `-32602`，后台失败为 `isError=true` 的工具结果；不透传后端错误正文。
- 最大输入帧 1 MiB、后台响应体 1 MiB、响应头 32 KiB；HTTP 总超时 40 秒，最多 4 个并发后台请求。超限不截断聊天 JSON，而是返回错误；缩小分页/关键词范围，大型微信会话列表可改用工作台。
- MCP 请求取消、stdin EOF 与运行上下文取消会中止桥接 HTTP 请求。已提交的导入不能靠取消撤销，不可把错误当成“未入库”的证明；先核对工作台结果，不盲目重试。既有搜索/消息 GET 后端未全量采用 context-aware SQL，HTTP 取消不保证其锁等待或查询即时停止。
- 昵称、消息、摘要都属于不可信内容；客户端/模型不得执行其中的提示词、任意链接、命令或凭据索取。

## 常见问题

| 现象 | 处理 |
|---|---|
| 客户端无法启动程序 | 先构建，再核对 `command` 为本机存在的绝对路径；更新源码后需重新构建并重启桥接进程 |
| 启动即报 token 配置错误 | 核对客户端及继承环境的显式 `SOCIALD_API_TOKEN`；空值也会优先失败。要用默认凭据须真正移除变量并重启客户端，不能赋空 |
| 启动即报凭据/ACL/URL 绑定错误 | 保留原文件与权限；核对同一 Windows 账号、KnownFolder/显式路径及后台实际端口，不删除密文、不放宽 ACL、不自动换 token |
| 连接成功但工具提示服务不可用 | 初始化不访问后台；确认启动脚本管理的后台仍在运行，端口正确 |
| 工具返回 HTTP 401 | 默认模式核对后台是否被显式 env token 覆盖或已轮换，再重启 MCP 加载当前凭据；env 模式安全更新注入值并重连，不假设文件已自动同步 |
| 轮换提示端口占用或 env 冲突 | 先在原终端正常停后台；核对并真正移除显式 env token。脚本不替用户停进程，不以强杀、改 ACL 或删文件绕过检查 |
| 轮换后浏览器不能恢复旧授权 | 这是预期：浏览器重新配对；MCP 重启加载新 token，显式 env 客户端更新受控值 |
| 微信工具返回 HTTP 503 | 先在产品工作台核对本人微信配置、源库状态及文件权限；不要向模型提交密钥或路径 |
| 响应过大 | 搜索/消息减小 `limit`；微信会话列表没有分页，超大列表改用工作台 |
| 导入后回复中断 | 先查看消息中心核实是否已保存；取消或错误不代表事务回滚 |

## 工程验证与待验收

自动测试仅使用显式合成令牌、临时加密数据库和本机测试服务，不读取真实配置或微信数据：

```powershell
# 服务凭据、MCP 桥接和真实命令入口的专项；Windows 才覆盖 DPAPI/ACL
# PowerShell/Go 互操作使用临时合成文件，不读取真实默认凭据
go test ./internal/localcredentials/... ./internal/mcp ./cmd/sociald-mcp
# 包含真实 HTTP 鉴权与临时 SQLCipher 存储的联调；需后台编译环境
go test -tags sqlite_fts5 ./tests/integration -run '^TestMCPBridge'
# 完整工程检查（不包含原生 Tauri 或真实账号验收）
.\scripts\check.ps1
```

检查结果、限制和人工验收步骤见 [T04 MCP 验收记录](acceptance/T04-mcp-validation.md)；决策见 [ADR 0012](adr/0012-mcp-local-bridge.md)。真实客户端接入、本人微信读取和指定非作者审查仍须单独登记，自动测试不能替代这些验收。
