# T06 微信正文关键词筛选验收记录

- 任务：`T06` / `FEAT-T06`
- 变更：`CHG-T06-010`
- 计划日期：2026-11-17（子项工程实现）；真实验收日期：—
- 负责人：angjustinl（后端 / AI）
- 后端审查：joey（待正式登记）
- 前端负责人：joey；前端审查：angjustinl（待正式登记）
- 产品、测试与交付验收：kirinbao（待真实账号执行）

## 验收范围

在读取本人 Windows 微信本地数据库时，可为所选私聊或群聊填写正文关键词。系统在现有最近记录窗口内进行 Unicode 不区分大小写的子串匹配，只把命中的可解码文本保存到加密消息中心。空关键词保持原读取行为。

## 工程检查

| 检查项 | 结果 | 证据 |
|---|---|---|
| 中文关键词命中和无命中 | 通过 | `internal/connector/wechatdb/reader_test.go`：`TestReadMatchingFiltersTextWithinBoundedSourceWindow` |
| 控制字符、超长关键词拒绝 | 通过 | reader/API 输入校验测试 |
| API 过滤后再入库 | 通过 | `internal/api/wechat_test.go`：`TestWechatReadKeywordFiltersBeforeImport` |
| 客户端请求体和组件转发 | 通过 | `scripts/test-wechat-monitor.mjs` |
| 前端类型检查 | 通过 | `npm.cmd --prefix apps/desktop run check` |
| OpenAPI 契约 | 通过 | `api/openapi.yaml` 已补充 keyword 请求/结果字段；`scripts/check.ps1` 全仓检查通过 |

## 真实验收步骤

使用用户本人已授权的 Windows 微信配置，重启 `scripts/start-inbox.ps1`，打开消息页：

1. 读取会话列表，选择一个私聊或群聊。
2. 输入一个已知中文关键词，读取并保存，确认只保存正文命中的消息。
3. 输入一个英文关键词，确认大小写变化仍能命中；输入不存在的词，确认结果为 0 条新增且不报源库错误。
4. 清空关键词重新读取，确认恢复原窗口行为；重复读取确认重复计数和已有消息不变。
5. 在统一消息中心按微信来源搜索，确认已经保存的命中消息可继续检索和查看。

## 通过标准

- 中文、英文、有命中、无命中、空关键词各至少验证 1 次。
- 关键词只影响本次读取窗口，不写入微信数据库，不改变账号/会话限域和发送关闭状态。
- 指定后端审查、前端审查和 kirinbao 真实验收记录完成后，才能填写实际完成日期并将子项标记为“已完成”。

## 当前结论

工程实现和合成检查已通过；真实微信私聊/群聊关键词读取、重复读取、页面验收和指定人员审查尚未完成。关键词筛选不等同于全历史搜索，完整检索能力仍按 T10 推进。
