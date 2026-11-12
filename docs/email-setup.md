# 邮箱接入与只读同步

首版面向 QQ、163 及支持 IMAP 应用授权码的企业邮箱。配置完成后，在统一消息中心点击“同步邮箱”，将最近一批邮件保存到本地加密库，可与其他消息一起筛选、搜索、标记已读、置顶和归档。

当前为**一个账号、一个文件夹的手动同步**，不持续自动抓取，不发信、不删除邮件，也不改变原邮箱的已读状态。本地管理状态与原邮箱分开。

## 准备邮箱

先由你本人在邮箱官方后台开启 IMAP，并获取第三方应用授权码。不要把授权码、邮箱密码或令牌发到聊天里，也不要写入仓库。

- QQ 邮箱通常使用 `imap.qq.com:993`，用户名填写完整邮箱地址，密码项填写应用授权码。QQ 官方说明第三方收信使用授权码，服务须由本人开启，改账号密码后旧授权码可能失效。[QQ 授权码说明](https://help.mail.qq.com/detail/106/985)、[加密连接参数](https://help.mail.qq.com/detail/0/1010)。
- 163 个人邮箱常见配置为 `imap.163.com:993`，实际主机与授权码设置以本人邮箱后台为准，不能混用个人、VIP 与企业邮箱地址。网易帮助站本轮未能读取，因此这些参数还需用你的账号核验；可参考[华为客户端的网易邮箱配置指南](https://consumer.huawei.com/cn/support/content/zh-cn15872099/)。
- 企业邮箱使用管理员提供的 IMAP 主机、TLS 端口和应用授权码。本轮仅支持有可验证 TLS 证书的公网目标，不支持内网主机、自签名证书、明文 IMAP 或 STARTTLS 降级。
- Gmail 的应用密码取决于两步验证与组织策略，首版尚未做真实兼容验证；Microsoft 365 / Outlook 要求 OAuth 的账号本轮不支持，不能用应用密码代替 OAuth。[Gmail 应用密码限制](https://support.google.com/accounts/answer/185833?hl=en)、[Microsoft 现代认证要求](https://support.microsoft.com/en-us/outlook/pop-imap-and-smtp-settings-for-outlook-com)。

支持服务端声明的 IMAP ID 扩展时，仅提交真实产品名称和版本，不发送操作系统或机器标识。服务器仍可能要求额外安全验证，须在邮箱官方后台自行完成。

## 本机配置

在项目根目录打开 PowerShell 7。将示例账号替换为自己的完整邮箱地址；企业或其他提供商同时替换主机：

```powershell
$env:EMAIL_IMAP_HOST = 'imap.qq.com'
$env:EMAIL_IMAP_PORT = '993'
$env:EMAIL_IMAP_USERNAME = 'your-address@qq.com'
$env:EMAIL_IMAP_FOLDER = 'INBOX'
$env:EMAIL_IMAP_LIMIT = '50'
$mailSecret = Read-Host '输入邮箱应用授权码' -AsSecureString
$mailPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($mailSecret)
try {
    $env:EMAIL_IMAP_PASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($mailPointer)
} finally {
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($mailPointer)
    $mailSecret.Dispose()
    Remove-Variable mailPointer, mailSecret
}
.\scripts\start-inbox.ps1
```

授权码进入当前后台进程的环境，不进入工作台或数据库；退出终端后需重新设置。仅运行 `.env.example` 不会加载配置文件。授权码中的格式分组空格是否应去除，以提供商实际规则为准；本轮只接受不含空白的可打印 ASCII 授权码。

`EMAIL_IMAP_LIMIT` 默认为 50，允许 1—100；文件夹默认为 INBOX。所有邮箱变量留空则不接入，部分缺失或错误配置会拒绝服务启动，并给出不含凭据的错误。

另开终端按根 README 启动前端。工作台设置使用的是本产品访问令牌，**不是邮箱授权码**；启动脚本会把本产品令牌复制到剪贴板。

## 同步与查看

1. 工作台连接本地服务，打开“消息”。邮箱面板显示已配置账号与文件夹。
2. 点击“同步邮箱”。每次读取当前文件夹最近 N 封邮件，结果显示新增、重复、跳过与是否只覆盖最近一批。
3. 点击“邮箱”来源，查看主题、发件人、收件人和纯文本正文。不同邮件各自保存，不根据同名或主题自动合并线程。
4. 可以在本地标记已读、置顶、归档。再次同步不会把原邮箱的未读标记强加回已有本地记录。
5. 等待超时、断网或“停止等待”后，先点“更新状态”和刷新消息，再决定是否重试；停止前端等待不承诺撤销已开始的本地保存。

状态 GET 只读本进程状态，不连接邮箱。最近成功同步时间和计数目前在进程内保留，重启后重置；邮件本身和本地管理状态保存在加密库中。

## 内容与范围限制

- 每次只收当前文件夹最近一批。较早邮件以及两次同步之间大量新增后落到窗口外的邮件，可能尚未收取；不承诺全量历史、无遗漏或自动补齐。
- 一封原始邮件上限 512 KiB，正文上限 16000 UTF-8 字节。服务端声明超限或 MIME/字符集/日期不支持的邮件跳过并计数；恶意协议、错误 UID 或不可信大字面量会终止整轮，本地不会提交部分批次。
- 纯文本优先；只有 HTML 时转为纯文本。不执行邮件脚本、打开链接或加载远程图。附件不提取、保存或打开，但原始邮件的传输可能包含附件字节。
- 邮件按服务器、端口、本人账号、文件夹、UIDVALIDITY 和 UID 隔离去重，不能用可伪造的 Message-ID 或发送者昵称判断同一封邮件。
- 升级已有数据库会在加密事务内迁移到 v2，保留原消息 ID、序号与本地管理状态。备份恢复与跨设备密钥迁移仍按后续 T24 处理。

同步异常时，核对本机网络、官方 IMAP 开关、TLS 参数、完整账号和应用授权码。不要关闭证书校验来处理连接失败。密码/授权码错误、网络错误与协议错误均以固定分类显示，工作台不会回显提供商原始错误。

本轮开发测试使用本地 TLS 模拟邮箱与合成邮件。QQ/163/企业账号真实认证、指定成员代码审查和谢麒麟的独立验收仍待完成。
