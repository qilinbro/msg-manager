# 数据迁移边界

T04 尚未选择 SQLCipher Go 驱动，因此不生成会被误认为已支持加密的数据库。

T03 验证 Go/SQLCipher、FTS5、WAL 与 Windows 构建后，再由 T05 实现版本化迁移、唯一约束及作用域。初始化必须在持久化能力就绪后更新 readiness；不得用内存列表替代联系人和消息存储。
