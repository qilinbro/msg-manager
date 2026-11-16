# QQ Milky 后台授权持久化验证

- 日期：2026-11-16（Asia/Shanghai）；关联 CHG-T02-004 / FEAT-T02。
- 负责人：angjustinl；后端审查：joey待审；产品/真实账号验收：kirinbao待验。

## 已验证

- SQLCipher v7迁移、账号隔离、空库默认关闭、保存/重开和禁用清理。
- 服务重启读取授权后进入 pending_verification；成功重新核验后恢复后台接收，构造 handler 不外呼。
- 未授权实时 peer 不入库；保存失败保留原授权/worker；核验失败停止内存 worker。
- go test -race -tags sqlite_fts5 ./internal/store ./internal/api ./tests/integration -run 'QQMilky|Milky|Migration|Wechat' -count=1 通过。

## 边界

合成协议端和SQLCipher临时库测试，不代表真实 Lagrange.Milky、本人 QQ、浏览器/Tauri 或真实网络验收。逐页历史水位、事件补发、发送/审批仍未实现；发送保持关闭。
