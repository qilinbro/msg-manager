# QQ Milky 协议端接入说明

日期：2026-11-15；关联 T02（`FEAT-T02` / `CHG-T02-003`），设计影响 T04、T06、T07、T08、T17。本说明描述已实现能力，不代表真实账号验收已完成。

## 产品定位

`qq-milky` 是 QQ 主线连接器。它在自有 Go 服务内实现 Milky 1.3 协议客户端，对接**你自己部署并登录本人 QQ 的独立协议端**（`Lagrange.Milky`）。本产品不安装、不启动、不代替协议端登录，不分发协议端二进制，也不接管你未授权的好友或群聊。

协议端为 GPL-3.0 许可的**外部独立进程**：本产品不链接、不复制其源码，只经本机 HTTP / WebSocket 调用公开协议接口。发布说明与交付清单须声明协议端名称、许可证与固定版本仓库链接；这是工程边界口径，法律结论由产品负责人确认。

一次身份核验只说明最近一次检测成功，**不等于持续在线，也不启用发送**。自动发送与平台发送接口始终关闭。

## 准备

1. 自行部署 `Lagrange.Milky`（`LagrangeDev/Lagrange.Core` 的 Milky 实现），确认其实现 `milky_version = 1.3`。版本不匹配时本产品明确拒绝连接。
2. 在协议端完成本人 QQ 登录。需要自行评估账号风控：第三方协议端可能需要签名服务，新注册账号可能被拒绝登录；本产品不绕过验证码或平台限制。
3. 协议端只监听本机回环地址，配置强随机 `AccessToken`，不要暴露到公网。本产品只允许 `http://127.0.0.1:<端口>` / `http://localhost:<端口>`，不解析 DNS、不跟随重定向与代理、令牌只放 `Authorization` 头。
4. 本产品只接受 `POST /api/{action}` 与正向 `GET /event`（WebSocket），不接受 WebHook 入站推送。

三类令牌互不相同：本产品 `SOCIALD_API_TOKEN`、协议端 `QQ_MILKY_ACCESS_TOKEN`、以及 QQ 账号本身的登录凭据。工作台只持有前者且仅存内存。

## 在后台配置

在启动 `sociald` 的 PowerShell 终端设置环境变量，`.env.example` 不会被自动读取：

```powershell
$env:SOCIALD_API_TOKEN = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$env:SOCIALD_DATABASE_PATH = '<加密库绝对路径>'
$env:SOCIALD_DATABASE_KEY = '<64 位十六进制随机密钥>'
$env:QQ_MILKY_URL = 'http://127.0.0.1:3000'
$env:QQ_MILKY_ACCESS_TOKEN = Read-Host -AsSecureString 'Milky AccessToken' | ForEach-Object {
  $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($_)
  try { [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) } finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
}
$env:QQ_MILKY_ACCOUNT_ID = '<本人 QQ 号>'
# 可选：单次历史拉取 1..30，默认 20
$env:QQ_MILKY_HISTORY_LIMIT = '20'
.\scripts\start-inbox.ps1   # 或 .\scripts\dev-backend.ps1
```

三个 `QQ_MILKY_*` 值全部留空时不接入。配置错误只在启动日志返回固定脱敏文案，不回显地址或令牌。

## 在工作台使用

1. 连接本地服务后，在「设置 → QQ Milky 协议端（主线）」查看状态并点「核验账号与版本」。核验调用 `get_login_info` 与 `get_impl_info`，同时校验登录 QQ 与预期账号、Milky 版本。
2. 填写**明确选定的好友 QQ 号**，点「同步一页历史」。首版仅好友私聊纯文本，单次最多 30 条；服务端生成 `milky:friend:<peer_id>:<message_seq>` 稳定标识并事务落库，重复同步去重；非文本与不支持场景只记录跳过原因。
3. 需要后台接收时，填写最多 20 个明确授权的私聊 QQ 号并点「开启后台接收」。默认关闭；开启前必须完成核验；服务重启后设置不持久化，须重新开启。
4. 状态区展示后台接收、累计跳过与最近缺口。Milky 事件没有唯一 ID、游标或断线补发，断线会记录缺口并在重连后用历史接口**有界补查**，界面不承诺取回全部历史。

## 接口

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/v1/connections/qq-milky/status` | 只读缓存状态，不触发网络调用 |
| GET | `/api/v1/connections/qq-milky/verify` | 账号与版本核验 |
| POST | `/api/v1/connections/qq-milky/sync` | 按联系人同步一页历史（`peer_id`、`limit`） |
| POST | `/api/v1/connections/qq-milky/monitor` | 显式开启/停止后台接收（`enabled`、`peer_ids`） |

全部接口沿用本机 Bearer 鉴权、Host/Origin 白名单与服务端派生的工作区作用域。请求体不能选择协议端地址、令牌或账号。错误只返回固定分类（如 `MILKY_IDENTITY_MISMATCH`、`MILKY_VERSION_MISMATCH`、`MILKY_UNREACHABLE`），不回显上游文本。契约见 `api/openapi.yaml`。

## 当前支持与已知限制

- 已实现：共享 `adapter` 本机传输底座、Milky 身份/版本/好友/历史/事件流客户端、工作台面板、加密库入库与去重、后台事件接收与缺口补查、契约与单元/集成测试。
- 发送：`send_private_message` 在协议客户端内已实现，**没有接入任何 HTTP 路由，也未装配发件箱执行器**。必须等 T15/T16/T17 的规则、审批与发件箱通过审查验收后才可讨论启用；契约 `sending_enabled` 固定为 `false`。
- 来源枚举：schema v6 的 `source` 兼容列仍记为 `import`，协议同步的可追溯性由服务端生成的 `milky:friend:` 前缀与连接显示名保证；独立来源枚举需后续 v7 迁移，登记为 T05 遗留项。
- 历史游标：`get_history_messages` 的 `next_message_seq` 已返回给调用方，但首版每次从最新一页同步，尚未实现逐页水位持久化与跨页增量。
- `Lagrange.Milky` README 注明 `message_scene=friend` 时 `start_message_seq` 不能为空，与规范「不提供则从最新开始」存在出入，需在真实协议端上验证。
- 群聊、临时会话、媒体正文、消息编辑/撤回事件、模型自动分析与审批均不在首版范围。
- 后台接收设置不持久化；电脑休眠、关机或后台退出后停止，不承诺离线期间的消息补齐。

## 真实接入验收清单（须由产品负责人在本机完成）

1. 固定 `Lagrange.Milky` 版本与提交标识，记录实现名称、版本与 `milky_version`。
2. 用本人授权 QQ 完成协议端登录，确认账号风控与签名服务授权边界。
3. 执行真实账号核验，确认 `account_id` 与预期一致、错误账号被拒绝。
4. 对一名授权联系人同步真实历史并在消息中心逐条对账；重复同步不新增。
5. 开启后台接收，发送真实文本消息，确认入库、断线缺口提示与重连后有界补查。
6. 核对工作台不出现任何协议端地址、令牌或上游错误正文；确认所有 `sending_enabled` 为 `false`。
7. 由 joey 审查后端、angjustinl 审查前端，kirinbao 组织验收并登记证据后，才更新任务状态与实际完成日期。

合成测试与工程检查不能替代上述真实授权与独立验收。
