# Telegram 个人账号接入

更新：2026-11-18。`CHG-T06-014` 增加个人账号后台增量接收，限用户明确授权的私聊文本。机器人 Token 不能代替个人账号登录。

## 第一次配置

1. 已有可登录的官方 Telegram 账号，打开 [my.telegram.org](https://my.telegram.org)，用带国家代码的手机号登录，按官方 Telegram 收到的验证码完成验证。进入 **API development tools**，填写应用信息，取得自己的 **API ID / API Hash**。应用名称可用 `Msg Manager`，简称可用自己的英文应用简称；平台按实际用途选择桌面。具体必填项以页面为准。[Telegram 官方申请说明](https://core.telegram.org/api/obtaining_api_id)。不要使用网上共享或示例 API 凭据。
2. 在项目目录运行 `scripts/prepare-telegram.ps1`，为 Telegram 单独准备 Python 环境和锁定的 Telethon 依赖，不影响微信初始化环境。
3. 运行 `scripts/initialize-telegram.ps1`，在本机终端输入自己的 API ID、API Hash、带国家代码的手机号、Telegram 收到的登录验证码；如启用了二步验证，按提示输入密码。敏感输入隐藏，不发到聊天或填入项目文件。
4. 核对终端显示的本人账号，确认后保存。工作台打开 **设置 → Telegram 个人账号**，核验账号、加载会话列表，明确勾选 1–10 个私聊，设置周期（30–900 秒，默认 60 秒），开启后台收件。

```powershell
cd D:\msg_manager
.\scripts\prepare-telegram.ps1
.\scripts\initialize-telegram.ps1
```

登录可能需要已登录设备上的验证码，具体由 Telegram 决定。本产品不会代收验证码、绕过二步验证或导出既有 Desktop 会话。API Hash 和 StringSession 均只留在本机，不提交到 Git 或模型。[Telethon 登录说明](https://docs.telethon.dev/en/stable/basic/signing-in.html)。

没有 API ID / API Hash 时，先完成第 1 步；准备运行环境可以提前进行，但不能把它当作账号已连接。申请报错时保留页面上的非敏感错误文本用于排查，避免反复提交或发送带凭据的截图。

## 使用与范围

- 页面状态只读取缓存，不触发平台读取。开启后由独立 Go 后台调用本机只读 helper，关闭工作台不影响读新消息。可用页面「暂停」随时停止。
- 列表是有界的最近会话窗口，筛出其中的个人私聊，排除机器人、自己和群组/频道；不等于整个通讯录。未出现在该窗口的联系人暂不支持，不通过手机号或用户名全局搜索补齐。
- 新选私聊先建立当前最新消息的基线，之后按消息 ID 从旧到新分批增量读取，重启继续同账号的既有水位。未出现成功检查前，不能把「已开启」当作已收件；基线以前的历史不会自动回填。增量读取依赖[Telethon 的消息迭代接口](https://docs.telethon.dev/en/stable/modules/client.html#telethon.client.messages.MessageMethods.iter_messages)。
- 首版不下载媒体、不读群组/频道/秘密聊天、不自动标已读、不发送。所选私聊必须仍可从当前账号的有界最近会话窗口定位；不可见时报告错误并保留水位，不用昵称或手机号猜测联系人。
- 消息保存到本产品加密库后可统一管理。本产品已有的联系人自动整理或日报若事先获得授权，可能随后使用这些新消息；Telegram 接入本身不授予模型权限。
- 本地同步按账号、联系人与消息 ID 去重；兼容库的来源仍记为 `import`，专用读取标识使用 `telegram:user:`，接口返回 `source_kind=telegram_user`。不能用来源兼容字段宣称完成实时监听或全历史恢复；手工导出记录与新同步来源可能需要对账。

## 本机凭据与网络

向导使用内存 `StringSession`，不会生成明文 `.session` SQLite 文件。API ID、API Hash、会话及预期账号写入 `data/private/telegram-user.dpapi`，使用当前 Windows 用户 DPAPI 保护，目录保持私有访问权限。API/页面不返回会话、API Hash、手机号、验证码或二步验证密码。[Telethon 会话说明](https://docs.telethon.dev/en/stable/concepts/sessions.html)。

会话凭据本身具有账号授权能力；“只读”是本产品开放操作的边界，并非 Telegram 签发了一个只读 Token。同一 Windows 用户的其他程序也可能具备使用其 DPAPI 的权限。删除本地文件不等于 Telegram 服务端撤销会话；需要撤销时在官方 Telegram **设置 → 设备** 中结束对应会话。

若当前网络不能直连，可在本机初始化时填写自己已运行的本地 SOCKS5 代理端口。连不上时核对代理程序和 Telegram 登录状态。后台遇到 Telegram 限流会记录最早下次读取时间，暂停、重新启用或重启不会缩短等待；不自动重试登录，不替用户重新授权。

## 故障排查

| 状态 / 提示 | 处理 |
|---|---|
| 未准备运行环境 | 运行 `prepare-telegram.ps1`，核对 Python 与依赖安装结果 |
| 尚未初始化 | 运行本机登录向导，不能只填 BotFather Token |
| 登录过期或被撤销 | 在本机重新初始化；如已有保存文件，按向导明确选择替换 |
| 账号不一致 | 停止当前操作，核对向导账号后重新初始化，不混用其他人的会话 |
| 访问失败 | 核对网络/可选本地 SOCKS5，不把错误当作授权成功 |
| 限流或积压 | 后台限速后继续读取，查看最后成功检查时间；不需要不停点击重试 |
| 同步超时 | 先检查消息中心结果，再决定是否再次同步；界面不自动重放 |

电脑休眠、关机或后台关闭后不会继续读取；后台重新启动后恢复已启用的同账号任务。真实个人账号、指定审查和原生桌面验收仍待执行，实际完成日期 —。
