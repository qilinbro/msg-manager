# 数据迁移边界

T03 已实际验证 Go/SQLCipher、FTS5、WAL、错误密钥拒绝与 Windows CGO 构建。T05 在此之后实现 `0001_messages.sql`：本地单工作区、四平台账号、联系人、会话、消息、索引和去重约束，迁移版本由加密库内的 schema_migrations 管理。

FTS5 表与触发器已创建，当前用户搜索使用限域字面子串。任务与发件箱表、全量备份恢复及正式审查未完成，不代表 T05 已整体验收。`/api/v1/inbox` 声明本地存储可用；全链路 readiness 仍503直到实时收件、任务和发送具备条件。

`0002_email.sql` 在事务内扩展邮箱平台及 import/imap 来源，保留旧资源 ID、消息序号和本地管理状态，重建 FTS；升级测试核对外键、旧数据和新增邮件。该升级不实现任务或发件箱表。

`0003_ai.sql` 增加限域 AI 设置、分析、记忆、证据与调用额度。`0004_wechat_source.sql` 仅为连接和消息新增受约束的 `connector_source` 列，不重建 v3 表、删除 AI 数据或更改 FTS。既有 `source` 列保留旧版兼容值；对外来源使用 `coalesce(connector_source, source)`：只读微信数据库采集为 `wechatdb`，普通文件导入仍为 `import`，同账号两种来源混用时连接为 `mixed`。重复消息保留首次存储来源和本地状态。微信来源只能通过服务端 `Store.ImportWeChat` 写入，导入请求不能设置该列。版本记录和新增列在同一事务提交，失败整体回滚。
