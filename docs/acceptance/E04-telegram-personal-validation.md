# Telegram 个人账号接入验证

日期：2026-11-17（Asia/Shanghai）。关联 `CHG-E04-002 / FEAT-E04`、`CHG-T06-011 / FEAT-T06`、`CHG-T08-008 / FEAT-T08`。

## 交付与责任

- 后端、凭据和只读适配：angjustinl；指定代码审查：joey。
- 前端、引导和会话选择：joey；指定代码审查：angjustinl。
- 本人账号测试验收与交付：kirinbao。正式审查人、正式验收日期、实际完成日期均为 **—**。
- 工程实现已通过检查，子项 **待指定审查 / 待真实验收**，整项任务保持进行中。代理交叉检查不计为指定成员审批。

## 已交付

- `scripts/prepare-telegram.ps1`：独立 Python 环境，固定 Telethon 1.45.0 与 python-socks 2.8.1；兼容从 PowerShell 5.1 转入 7.2+。
- `scripts/initialize-telegram.ps1`、`scripts/telegram_user/worker.py`：仅本机交互登录，输入 API ID / Hash、手机号、验证码及可选二步验证；昵称与账号 ID 核对后确认保存。
- `data/private/telegram-user.dpapi` 是初始化后固定的本机保存位置，仓库不包含该文件或真人凭据。内存 StringSession、CurrentUser DPAPI、独立熵及私有 DACL；不生成明文 `.session`。
- `internal/connector/telegramuser/`：固定 helper 命令、最小子进程环境、超时取消、2 MiB 输出限制及结构校验；不把其他平台或模型密钥传入 helper。
- `internal/api/telegram.go`：缓存状态、显式核验、最近个人私聊列表、选定私聊同步。单次 1—100 条文本，明确选择后加密入库，按账号/联系人/消息 ID 去重。
- `apps/desktop/src/TelegramPanel.tsx`、`telegram-api.ts`、`telegram.css`：本机配置引导、账号核验、私聊选择、同步结果；页面不收集登录秘密。
- [接入步骤](../telegram-setup.md)、OpenAPI、检查脚本及三处进度文件已同步。

## 工程检查结果

- `scripts/check.ps1 -IncludeTelegram` **通过**：Go 格式、全部 Go 测试、vet、OpenAPI、PowerShell bootstrap 9 个子用例 / launcher 53 次合成启动；全部客户端检查、服务会话 11 组 / 设备授权 31 组、前端格式 / 类型 / 构建。
- Telegram Python **28 项通过**：严格输入、脱敏输出、私聊限域、真假账号拒收、Windows DPAPI/独立熵、私有 DACL、原子替换保留旧文件、登录迁移、二步验证及取消保存后的撤销结果。
- 非缓存 `-race -tags sqlite_fts5 -count=1` 检查 **通过**：Telegram connector、API、Store、Config、Connector catalog；账号绑定修复后另行重跑 Telegram API race，通过。
- 前端构建：JS 433.55 kB，CSS 49.65 kB；未运行原生 Tauri 打包或真实账号登录验收。
- 本机独立 Telegram 依赖已准备，准备阶段没有登录账号、导入或发送聊天。

## 交叉检查与返工

1. 修复 `log_out()` 返回 `False` 被当作成功：未保存登录无法撤销时明确提示在 Telegram“设置 → 设备”检查。
2. 修复首次登录 DC 迁移：仅对服务端明确迁移错误重试一次；FloodWait、验证码错误及不确定网络失败不自动重试。日常读取保持零请求重试。
3. 修复旧窗口换号竞态：同步必填 `expected_account_id`，锁内对比当前核验账号；不符返回 409、零 helper 调用 / 零入库，保留新窗口合法状态。

## 范围与待验收

最近会话扫描窗口为 100，筛选其中的个人私聊；排除机器人、自己、已删除用户、群组与频道。同步文本不下载媒体、不发送、不做平台已读确认，也未实现 Telegram 持续后台接收或全历史恢复。兼容存储来源暂为 `import`，专用外部 ID 为 `telegram:user:`，接口明确返回 `source_kind=telegram_user`；与手工导出来源可能需要对账。

用户确认尚无 API ID / Hash，当前 **未初始化真实 Telegram 账号**。后续由 kirinbao 在本机完成：

- [ ] 申请本人应用，终端登录并核对本人账号；页面不出现 API Hash、会话、手机号、验证码或密码。
- [ ] 可选本地 SOCKS5 网络下成功核验；无配置、登录撤销、限流和断网均如实提示。
- [ ] 选定私聊同步，消息页文本与方向正确；重复同步无新增重复、保留本地已读状态。
- [ ] 验证群聊 / 机器人不进入选择列表，无消息发送或平台已读变化。
- [ ] joey 审后端、angjustinl 审前端；kirinbao 登记验收结果后再填写实际完成日期。

自动检查不是平台授权成功或真人验收的替代证据。

## 本机运行目录验证（2026-11-17）

Telegram 改动选择性同步至 `D:\msg_manager`，保留该目录较早的服务鉴权基线及其他工作。运行目录的 Telegram / Milky / service-session 客户端检查、Go API / Store / Config / Connector / integration 检查以及前端类型 / 构建通过（JS 418.12 kB，CSS 49.02 kB）。后台已重启，缓存接口实测：helper_available=true、configured=false、state=requires_initialization、read_only=true、sending_enabled=false；加密库启用、工作台 HTTP 200。只核对本机元数据，未登录或请求 Telegram 平台。运行目录旧启动器重启会更换服务令牌并复制到剪贴板，用户可能需在设置页重连。
