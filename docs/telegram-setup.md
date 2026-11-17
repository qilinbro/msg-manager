# Telegram 个人账号接入

日期：2026-11-17。关联 `CHG-E04-002 / CHG-T06-011 / CHG-T08-008`。本轮接入本人 Telegram 个人账号，首版只开放明确选择的私聊文本读取；机器人 Token 不能代替个人账号登录。

## 第一次配置

1. 已有可登录的官方 Telegram 账号，打开 [my.telegram.org](https://my.telegram.org)，用带国家代码的手机号登录，按官方 Telegram 收到的验证码完成验证。进入 **API development tools**，填写应用信息，取得自己的 **API ID / API Hash**。应用名称可用 `Msg Manager`，简称可用自己的英文应用简称；平台按实际用途选择桌面。具体必填项以页面为准。[Telegram 官方申请说明](https://core.telegram.org/api/obtaining_api_id)。不要使用网上共享或示例 API 凭据。
2. 在项目目录运行 `scripts/prepare-telegram.ps1`，为 Telegram 单独准备 Python 环境和锁定的 Telethon 依赖，不影响微信初始化环境。
3. 运行 `scripts/initialize-telegram.ps1`，在本机终端输入自己的 API ID、API Hash、带国家代码的手机号、Telegram 收到的登录验证码；如启用了二步验证，按提示输入密码。敏感输入隐藏，不发到聊天或填入项目文件。
4. 核对终端显示的本人账号，确认后保存。完成后打开工作台的 **设置 → Telegram 个人账号**，依次点击核验、读取会话列表，选择一位联系人同步最近文本。

```powershell
cd D:\msg_manager
.\scripts\prepare-telegram.ps1
.\scripts\initialize-telegram.ps1
```

登录可能需要已登录设备上的验证码，具体由 Telegram 决定。本产品不会代收验证码、绕过二步验证或导出既有 Desktop 会话。API Hash 和 StringSession 均只留在本机，不提交到 Git 或模型。[Telethon 登录说明](https://docs.telethon.dev/en/stable/basic/signing-in.html)。

没有 API ID / API Hash 时，先完成第 1 步；准备运行环境可以提前进行，但不能把它当作账号已连接。申请报错时保留页面上的非敏感错误文本用于排查，避免反复提交或发送带凭据的截图。

## 使用与范围

- 页面打开只读取缓存状态；只有显式核验、读取列表或同步才访问 Telegram。
- 列表是有界的最近会话窗口，筛出其中的个人私聊，排除机器人、自己和群组/频道；不等于整个通讯录。未出现在该窗口的联系人暂不支持，不通过手机号或用户名全局搜索补齐。
- 单次只同步选定私聊最近 1—100 条文本，不下载媒体，不请求群组/频道/秘密聊天，不自动标已读、不发送、不做后台持续接收。历史窗口之外的记录不保证同步。
- 消息保存到本产品加密库后可统一管理。本产品已有的联系人自动整理或日报若事先获得授权，可能随后使用这些新消息；Telegram 接入本身不授予模型权限。
- 本地同步按账号、联系人与消息 ID 去重；兼容库的来源仍记为 `import`，专用读取标识使用 `telegram:user:`，接口返回 `source_kind=telegram_user`。不能用来源兼容字段宣称完成实时监听或全历史恢复；手工导出记录与新同步来源可能需要对账。

## 本机凭据与网络

向导使用内存 `StringSession`，不会生成明文 `.session` SQLite 文件。API ID、API Hash、会话及预期账号写入 `data/private/telegram-user.dpapi`，使用当前 Windows 用户 DPAPI 保护，目录保持私有访问权限。API/页面不返回会话、API Hash、手机号、验证码或二步验证密码。[Telethon 会话说明](https://docs.telethon.dev/en/stable/concepts/sessions.html)。

会话凭据本身具有账号授权能力；“只读”是本产品开放操作的边界，并非 Telegram 签发了一个只读 Token。同一 Windows 用户的其他程序也可能具备使用其 DPAPI 的权限。删除本地文件不等于 Telegram 服务端撤销会话；需要撤销时在官方 Telegram **设置 → 设备** 中结束对应会话。

若当前网络不能直连，可在本机初始化时填写自己的本地 SOCKS5 代理端口；仅连接 `127.0.0.1`，不扫描代理、不下载代理工具，不把系统 HTTP 代理当作 MTProto 代理。连不上时核对代理程序和 Telegram 登录状态。平台要求等待时显示暂缓提示，不自动重试登录/同步，也不长时间占用后台睡眠重试。

## 故障排查

| 状态 / 提示 | 处理 |
|---|---|
| 未准备运行环境 | 运行 `prepare-telegram.ps1`，核对 Python 与依赖安装结果 |
| 尚未初始化 | 运行本机登录向导，不能只填 BotFather Token |
| 登录过期或被撤销 | 在本机重新初始化；如已有保存文件，按向导明确选择替换 |
| 账号不一致 | 停止当前操作，核对向导账号后重新初始化，不混用其他人的会话 |
| 访问失败 | 核对网络/可选本地 SOCKS5，不把错误当作授权成功 |
| 等待一段时间 | 按显示秒数等待，再由用户重新操作 |
| 同步超时 | 先检查消息中心结果，再决定是否再次同步；界面不自动重放 |

后台独立于工作台，但当前 Telegram 首版是手动操作，不是常驻收件。电脑休眠、关机或后台关闭后不会继续工作。真实个人账号、独立审查和原生桌面验收完成前，实际完成日期保持 —。
