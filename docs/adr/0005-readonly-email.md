# ADR 0005：邮箱作为统一消息来源，先提供手动只读同步

- 日期：2026-11-12。
- 关联：FEAT-E04 / CHG-E04-001、FEAT-T06 / CHG-T06-001、FEAT-T05 / CHG-T05-002、FEAT-T08 / CHG-T08-002。
- 用户确认：优先 QQ、163 及支持应用授权码的企业邮箱。

新增服务端配置的 IMAP 适配器，不依赖外部邮箱插件。初版只读手动同步最近 N 封邮件，复用现有加密收件箱、检索和管理功能；后台轮询、OAuth、附件和 SMTP 发信独立排期，不预填为完成。

协议只使用隐式 TLS，验证证书与主机，解析并固定公网目标 IP，禁止代理、私网和转向其他主机。操作具有总截止时间、单并发门控、响应大小、行长、字面量及跨字面量嵌套上限。邮箱主机、账号与授权码仅来自后台环境，浏览器不能指定目标。只使用 LOGIN、CAPABILITY、可选固定产品 ID、EXAMINE、FETCH 与 UID FETCH；没有 STORE、EXPUNGE、CLOSE 或 SMTP 执行能力。正文以 BODY.PEEK 读取，EXAMINE 未确认只读则拒绝保存。

依赖采用稳定 `go-imap v1.2.1` 的命令/响应解析与有界串行会话、`go-message v0.18.2` 的 MIME/字符集解码、`x/net/html` 的纯文本提取。MIME 不执行 HTML 或外部网络请求。超大/不支持邮件跳过并计数；协议不可信则整轮失败，不部分提交。

账户内部标识绑定服务器、端口和原始用户名；文件夹、UIDVALIDITY 与 UID 共同确定邮件身份。一封邮件对应一个会话，主题只作展示，不猜测线程，也不根据发送者昵称合并联系人。元数据与文本均进入 SQLCipher，授权码不持久化到数据库。源记录保留 import/imap 区别，平台能力不冒称持续在线。

v2 迁移通过新建约束图、复制旧行、子表先删除、重命名新表和重建 FTS 索引完成，事务失败回滚；不使用 writable_schema，旧资源 ID、序号、读/置顶/归档状态保留。重复邮件不覆盖本地读状态。

最近同步统计只在当前进程内保存；真实邮件和本地管理状态持久化。UIDVALIDITY 改变时生成新的协议身份，不静默把新邮件覆盖旧记录。有限窗口与可能缺口必须在界面和说明中呈现。

依据：[IMAP RFC 9051](https://www.rfc-editor.org/rfc/rfc9051.html)、[ID RFC 2971](https://www.rfc-editor.org/rfc/rfc2971.html)、[go-imap 官方稳定版本](https://github.com/emersion/go-imap/tree/v1.2.1)、[go-message](https://github.com/emersion/go-message/tree/v0.18.2)。
