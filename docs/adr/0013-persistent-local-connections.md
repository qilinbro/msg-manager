# ADR 0013：本机工作台与 MCP 的可撤销持久连接

- 日期：2026-11-17（Asia/Shanghai）。
- 关联：FEAT-T04 / CHG-T04-006，FEAT-T08 / CHG-T08-006。
- 状态：已实施，最终工程回归与有界隔离浏览器检查通过，待审查 / 待验收；指定非作者审查与真实验收待进行，实际完成日期 —。
- 职责：angjustinl 负责后端、存储与 MCP，joey 负责前端；后端由 joey、前端由 angjustinl 正式审查，kirinbao 组织验收。AI 辅助复核不替代这些审批。

## 背景与决定

反复启动生成新令牌、复制到浏览器和 MCP 客户端容易出错。解决方式不是关闭鉴权，也不是把主令牌写入浏览器存储或客户端 JSON：

1. Windows 启动器复用 **CurrentUser DPAPI 加密的稳定服务凭据**；MCP 在未提供显式 env token 时读取同一记录。
2. 浏览器经一次主令牌配对，保存 **不可导出的 P-256 私钥**；之后以一次性挑战证明持有私钥，换取仅内存保留的短时访问令牌。
3. 设备授权可到期、单独撤销；主令牌或实际监听地址变化时，启动即物理清理旧授权，不允许 A→B→A 恢复。
4. 不支持安全持久化时明确退化为**当前页面内存连接**；刷新需要重新输入。旧版 sessionStorage 主令牌会清除，不自动升级成长效授权。

## 浏览器协议与保存内容

实现见 [设备协议](<../../internal/api/local_devices.go>)、[迁移](<../../migrations/0012_local_devices.sql>)、[契约](<../../api/openapi.yaml>)。

| 位置 | 保存内容 | 有效期与边界 |
|---|---|---|
| 浏览器 IndexedDB | `extractable=false` 的 P-256 私钥、设备 ID、绑定的服务/前端来源与到期时间 | 默认 90 天；同源标签页可恢复。私密模式、站点清理或浏览器策略可能使其不可持久化 |
| SQLCipher | 设备公钥、随机 ID、服务/前端来源、issuer 指纹、创建/到期时间 | 每个本地工作区最多 32 项；不保存设备私钥 |
| 页面内存 | 15 分钟以内且不超过 grant 到期时间的 bearer；手动模式输入的主令牌 | 不写 localStorage、sessionStorage、URL、日志或 IndexedDB；手动模式刷新失去连接 |
| React 连接身份 | `device:<22字符设备ID>` 的稳定非秘密句柄 | 不是 bearer，不能直接用于 API；短令牌轮换不重挂业务页面或清空草稿 |

### 配对与证明

- `POST /api/v1/auth/devices` 必须同时提供主 Bearer 和明确允许的非空 Origin。设备 bearer 不能新增长效授权。
- 浏览器先验证非导出私钥可结构化克隆并用于签名，再执行一次注册和短会话证明；完整成功后才原子保存新 descriptor、发布连接。失败时保留原有 descriptor，已知新 grant 尽力撤销。
- `POST /api/v1/auth/challenges` 及 `POST /api/v1/auth/sessions` 是仅有的两个匿名证明入口，不使用 cookie。Host、Origin、工作区声明、严格 JSON 与 4 KiB 上限仍适用。
- challenge 有效 60 秒，进程内最多 256 项；满载返回 429 / `AUTH_BUSY` 和 `Retry-After: 60`，不无限增长。
- 签名消息精确为以下七行，无结尾换行：

```text
sociald-device-auth-v1
<规范服务 origin>
<精确前端 Origin>
<device_id>
<challenge_id>
<nonce>
<expires_at，RFC3339>
```

- ECDSA SHA-256 签名使用固定 64 字节 P1363，再作无 padding 的规范 base64url。服务端在验签前原子消费匹配 challenge；错误签名、并发和重放不能二次使用。
- 短 bearer 的 HMAC 密钥从主令牌秘密哈希取得，并使用独立 domain separator；**不能使用数据库中的公开 issuer 指纹作为 MAC 密钥**。
- 每个设备业务请求重新读取加密授权，核对来源、issuer 和到期。撤销阻止后续请求，不承诺回滚已通过鉴权的在途操作。
- 只有规范 `127.0.0.1:<port>` 监听支持浏览器授权。其他通过原配置校验的 loopback 监听保留主令牌 API，但设备配对/证明返回 503，设备 bearer 不接受。issuer 同时绑定真实 `cfg.Addr`，同端口换到 `127.0.0.2` 或 `::1` 也会启动清理，不能让旧监听上的代理中继旧 origin 的证明。
- 后台先独占绑定监听端口，成功后才构造 handler/打开库/清理旧 grant；重复实例若因端口占用启动失败，不得改变现有服务的设备授权。handler 构造失败必须释放端口。

### 恢复、并发与失败语义

- 恢复只建立短会话并 GET 系统状态，不重放导入、同步、分析、发送或配置操作。多个并发请求共享一次刷新；迟到的旧 401 不能覆盖新会话。
- 设备 GET/HEAD 最多刷新并重试一次。变更请求遇到 401 可以刷新凭据，但不自动再提交，返回 `AUTH_RETRY_REQUIRED`，用户核对后主动重试。
- 403 不刷新；断网、429、503 等暂时故障保留私钥供以后恢复。只有本地明确到期或 challenge 入口明确返回 `DEVICE_UNAUTHORIZED` 才确认 grant 无效/撤销并要求重新配对；generic 401 不作为删除长期密钥的证据。
- 短期 proof 401 可能只是 60 秒挑战过期或后台重启丢失缓存，因此至多重取一次 challenge 并签名；连续 proof 401 保留长期密钥并报告可恢复故障。不增加业务请求重试次数，也不重放变更。GET/HEAD 唯一重试仍为 401 时同样保留密钥，等待下一次明确验证。
- 忘记操作先取消本地恢复/刷新，再按设备 ID 条件删除旧记录；使用跨标签页通知，防止迟到请求或旧页面清掉新授权。
- `DELETE /api/v1/auth/devices/{device_id}` 允许设备撤销自身、主令牌撤销任意 canonical ID。无缓存 access 时可作一次有界证明，仅用于 DELETE，不恢复业务连接。
- 离线忘记必须显示“本地已忘记，远端撤销未确认”。浏览器删除站点数据不等于服务端撤销，其他标签页可能仍有短令牌。
- 无设备列表/批量管理界面。注册成功但响应丢失可能留下未知 grant；达到 32 项时不能静默绕过，需用仍有授权的页面逐一忘记，或正常停后台后显式轮换主令牌撤销全部。未撤销残留项最长持续到 90 天到期。

## Windows 启动器与 MCP

实现见 [PowerShell 凭据模块](<../../scripts/ServiceCredentials.psm1>)、[MCP 接入](<../mcp-setup.md>)、[ADR 0012](<0012-mcp-local-bridge.md>)。

- 默认文件位于 Windows Known Folder LocalAppData 下 `SocialIntelligenceOS\credentials\service-v1.dpapi`，不把可伪造的 `LOCALAPPDATA` 环境值当默认路径权威。可由 `SOCIALD_CREDENTIAL_FILE` 指向受保护的本机固定磁盘绝对路径。
- 文件是 CurrentUser / UI-forbidden DPAPI 密文；额外 entropy 为 UTF-8 `msg-manager/service-credential/v1`，明文结构为版本 1、规范服务 URL、API token。拒绝 UNC、ADS、链接/reparse、宽松 owner/DACL、超限和不合法结构。
- PowerShell 是唯一写入者，Go MCP 原生读取，不调用 PowerShell 获取令牌、不读数据库或浏览器密钥。坏文件、错用户、拒绝访问、URL 不符均失败，不覆盖、不自动修复、不退化到其他文件。
- token env **存在**时优先；空值、短值和含空白也算存在，必须失败，不能暗中读文件。确需回到受保护默认来源时，使用 `Remove-Item Env:SOCIALD_API_TOKEN` 真正移除变量。
- 正常启动复用合法已有记录；默认不打印 token、不触碰剪贴板。只有 `-CopyApiToken` 才显式复制，供首次/再次浏览器配对，并提示剪贴板历史风险。
- 持久启动器只接受规范 `SOCIALD_ADDR=127.0.0.1:<port>`，MCP 的 `SOCIALD_URL` 不是后台监听配置。非 Windows MCP 仍只支持显式 env；开发用主令牌路径保留。
- `-RotateApiToken` 必须有合法、URL 匹配的原记录，拒绝任意显式 env token；操作人须先正常停止后台。轮换在写入期间独占绑定真实端口，已占用即拒绝；不 HTTP 探测/操作现有服务、不强杀或自动重启它。轮换后重启 MCP、更新显式 env 客户端、浏览器重新配对。正常重启不是轮换。

## 不承诺的边界

- DPAPI 不是同一 Windows 用户下恶意软件的防线；不可导出私钥也不能阻止同源 XSS 代为签名或读取页面内存。必须继续维护本机用户、工作台来源与脚本安全。
- DPAPI/ACL 只保护凭据落盘。保留的明文 loopback HTTP 不提供服务器身份或 OS 用户隔离，无法防止不可信本机进程（包括其他 OS 用户）在后台停机时抢占同端口伪装服务；MCP 会向所配置的端点发送 bearer。应仅用于可信个人电脑，不把文件权限、禁代理/重定向误认为完整的本机端点认证。本轮不新增 TLS、命名管道或服务身份协议。
- 设备授权覆盖当前本地工作区的现有操作，不是按联系人/工具的细粒度授权。MCP 仍只有原有六个固定工具，不增加发送、任意 URL、SQL、密钥读取或配置权限；导入工具并非只读，可能触发已授权 AI 整理。
- 持久连接不等于自动安装 Windows 服务、开机启动、关机/休眠仍在线或平台账号长期在线。后台必须独立运行，自动发送仍关闭。
- 不搬移或重建用户现有数据库/私密密钥；首次升级应由操作人正常停旧后台、更新代码/构建后再按新流程启动，不暗中接管正在运行的实例。

## 验证与状态

可重复合成检查及真实验收清单见 [T08 持久设备验证](<../acceptance/T08-persistent-device-validation.md>) 与 [T04 MCP 验证](<../acceptance/T04-mcp-validation.md>)。已有后端/存储专项及辅助安全复核不构成正式审批；前端、启动器、完整工程检查与有界浏览器结果以验证记录中的实际结果为准。
