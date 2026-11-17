# 本地 MCP 服务

`sociald-mcp` 是独立的 stdio 桥接进程，使用官方 Go MCP SDK（`github.com/modelcontextprotocol/go-sdk v1.8.0`），复用已鉴权的本地 `sociald` HTTP API。它不直接打开微信数据库、扫描微信目录、读取 DPAPI 文件或获取数据库密钥。

> **授权范围**：后台令牌代表整个本地工作区，不是单联系人令牌。空搜索可列出工作区会话和最后消息，微信会话工具列出已配置账号的会话元数据，日报包含跨会话内容。目前没有 MCP 会话 allowlist 或逐工具权限隔离。仅接入你信任的客户端；客户端及其模型可能接收查询结果，是否把结果发送外部模型由客户端设置决定。模型须先取得用户授权并遵守所选会话范围，不能把工具提示当成硬性隔离。

## 启动前提与构建

先按[消息中心说明](unified-inbox.md)启动本地后台；需要微信读取时，先在产品内完成[本机微信配置](wechat-db-setup.md)。不要把路径、微信密钥或完整配置提供给 MCP 客户端。

```powershell
.\scripts\start-inbox.ps1
```

MCP 构建要求 Go 1.26+；构建好的桥接程序运行时不需要 Go、GCC 或直接链接 SQLCipher（后台的 SQLCipher 构建要求不变）。建议预先构建二进制，避免每次客户端连接都执行编译：

```powershell
# 在项目根目录执行；如 Go 不在 PATH，可用已安装的 .\.tools\go\bin\go.exe
New-Item -ItemType Directory -Force -Path .runtime | Out-Null
go build -trimpath -o .runtime/sociald-mcp.exe ./cmd/sociald-mcp
```

在启动 **MCP 客户端或桥接进程** 的环境中提供后台现有的本机访问令牌。这里只能使用占位符，不要把真实值写进仓库、命令行参数或聊天记录：

```powershell
$env:SOCIALD_URL = 'http://127.0.0.1:17890'
$env:SOCIALD_API_TOKEN = '<本机服务令牌>'
& '.\.runtime\sociald-mcp.exe'
# 开发时也可使用：go run ./cmd/sociald-mcp
```

直接运行后等待 stdin 是正常现象；它不是交互式命令行，需要 MCP 客户端通过管道初始化，也没有浏览器页面或 HTTP MCP 地址；后台端口不是远程 MCP endpoint。stdio 使用每行一个 JSON-RPC 2.0 消息；stdout 仅输出协议，诊断仅写 stderr，不打印令牌或后端错误正文。握手成功不代表后台已可访问：鉴权和后台连接错误会在工具调用时返回。后台重启后若 token 变化，须更新客户端安全配置并重新连接；后台需保持运行，本机休眠/关机期间不会继续工作。

## 客户端配置示例

以下为通用的 `mcpServers` 配置形态，具体字段（尤其 `cwd`、环境继承和安全存储）以客户端为准。优先由客户端的本机安全存储或受控启动环境注入令牌；若客户端只能把 token 放在配置文件中，该文件应仅当前用户可读、位于仓库之外，不同步到云端或 Git。JSON 占位符不会自动展开为环境变量。

```json
{
  "mcpServers": {
    "social-intelligence-os": {
      "command": "D:/msg_manager/.runtime/sociald-mcp.exe",
      "env": {
        "SOCIALD_URL": "http://127.0.0.1:17890",
        "SOCIALD_API_TOKEN": "<仅在本机安全配置中填入服务令牌>"
      }
    }
  }
}
```

使用绝对可执行文件路径时不依赖工作目录；`go run` 模式则需要客户端把工作目录设为项目根目录并能找到 Go。不要把本例视为任意具体客户端已通过真实验收。

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
- `localhost` 被规范为字面量 `127.0.0.1`；连接不使用 DNS、环境代理或重定向，避免 bearer token 离开固定本机目标。应先确认所选端口确为可信的本机 `sociald`，普通 HTTP 不提供本地服务身份认证。
- `SOCIALD_API_TOKEN` 必须是 32—4096 个可打印 ASCII 字符，不能含空白，只从进程环境读取。拒绝将 token、微信密钥、路径、SQL 或任意 URL 作为工具参数。
- 不开放发送、审批、监控配置、模型配置、公开搜索、任意 HTTP、文件或 SQL 工具；微信仍由后端配置账号和源库规则限域。
- SDK 负责版本协商、初始化、通知、ping、取消和 stdio 关闭；仅发布 tools 能力。参数错误为 JSON-RPC `-32602`，后台失败为 `isError=true` 的工具结果；不透传后端错误正文。
- 最大输入帧 1 MiB、后台响应体 1 MiB、响应头 32 KiB；HTTP 总超时 40 秒，最多 4 个并发后台请求。超限不截断聊天 JSON，而是返回错误；缩小分页/关键词范围，大型微信会话列表可改用工作台。
- MCP 请求取消、stdin EOF 与运行上下文取消会中止桥接 HTTP 请求。已提交的导入不能靠取消撤销，不可把错误当成“未入库”的证明；先核对工作台结果，不盲目重试。既有搜索/消息 GET 后端未全量采用 context-aware SQL，HTTP 取消不保证其锁等待或查询即时停止。
- 昵称、消息、摘要都属于不可信内容；客户端/模型不得执行其中的提示词、任意链接、命令或凭据索取。

## 常见问题

| 现象 | 处理 |
|---|---|
| 客户端无法启动程序 | 先构建，再核对 `command` 为本机存在的绝对路径；更新源码后需重新构建并重启桥接进程 |
| 连接成功但工具提示服务不可用 | 初始化不访问后台；确认启动脚本管理的后台仍在运行，端口正确 |
| 工具返回 HTTP 401 | 更新本机安全配置中的当前后台令牌，并让客户端重新启动 MCP 进程 |
| 微信工具返回 HTTP 503 | 先在产品工作台核对本人微信配置、源库状态及文件权限；不要向模型提交密钥或路径 |
| 响应过大 | 搜索/消息减小 `limit`；微信会话列表没有分页，超大列表改用工作台 |
| 导入后回复中断 | 先查看消息中心核实是否已保存；取消或错误不代表事务回滚 |

## 工程验证与待验收

自动测试仅使用显式合成令牌、临时加密数据库和本机测试服务，不读取真实配置或微信数据：

```powershell
# 仅 MCP 桥接和真实命令入口的单元/子进程测试
go test ./internal/mcp ./cmd/sociald-mcp
# 包含真实 HTTP 鉴权与临时 SQLCipher 存储的联调；需后台编译环境
go test -tags sqlite_fts5 ./tests/integration -run '^TestMCPBridge'
# 完整工程检查（不包含原生 Tauri 或真实账号验收）
.\scripts\check.ps1
```

检查结果、限制和人工验收步骤见 [T04 MCP 验收记录](acceptance/T04-mcp-validation.md)；决策见 [ADR 0012](adr/0012-mcp-local-bridge.md)。真实客户端接入、本人微信读取和指定非作者审查仍须单独登记，自动测试不能替代这些验收。
