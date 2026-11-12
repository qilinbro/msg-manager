# ADR 0004：先统一管理消息，在线适配逐个平台验证

- 日期：2026-11-12；关联 T03/T05/T07/T08。
- 用户要求：先将所有平台的消息统一管理做出来。

以平台无关的会话与消息仓储为核心，QQ、微信、Telegram、闪聊使用同一套本地管理能力。先支持用户主动导入文本记录，让列表、检索、未读、置顶、归档和历史查看形成可用路径；在线协议连接继续独立验证，不以导入冒充实时接管。

SQLCipher 原型在 Windows/GCC 实际验证后，才建立加密库迁移并接入业务。凭据仍留在后台，本轮密钥由当前 Windows 用户 DPAPI 保护；API 访问令牌独立。数据库/索引/WAL 使用加密存储，无明文回退。

连接以平台和本人账号限定，联系人/会话以对方稳定 ID 限定，消息按会话和原消息 ID 去重；昵称只作展示，不自动跨账号合并。相同标识存在内容冲突则整批回滚，不静默修改历史。

统一导入上限 8 MiB / 1000 条；可选 Telegram 单个个人私聊导出转换，媒体跳过并计数。所有输入视为数据，正文不会作为指令执行，也不会传给模型。搜索在当前工作区与平台/归档筛选内做字面子串匹配；模型的联系人限域检索仍是独立能力，不能借全局消息搜索绕过其范围。

仅开放明确的本地写路径：POST /imports/messages、POST /imports/telegram、PATCH /conversations/{id}。所有路径沿用 Host/Origin、Bearer 与固定工作区约束。平台发送、审批和自动化保持关闭。

本地消息可用状态由 /inbox 的 storage_enabled 声明；/health/ready 继续表示无人值守全链路就绪，两者不能混淆。列表采用手动刷新与显式分页；全局版本改变时旧游标拒绝，界面重载第一页。SSE 待持续事件与恢复底座完成。

本轮不宣称完成所有平台的实时接收。正式代码审查、真实授权、用户验收与原生打包仍保留；原计划保留为基线，由kirinbao确认统一消息优先后的修订排期。

依据：[SQLCipher Go 驱动固定版本](https://github.com/mutecomm/go-sqlcipher/tree/v4.4.2)、[SQLCipher 设计](https://www.zetetic.net/sqlcipher/design/)、[Telegram 导出](https://telegram.org/blog/export-and-more)。
