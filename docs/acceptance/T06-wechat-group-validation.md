# T06 微信群聊文本扩展验收记录

- 任务：`T06` / `FEAT-T06`
- 变更：`CHG-T06-009`
- 计划日期：2026-11-17（子项工程实现）；真实验收日期：—
- 负责人：angjustinl（后端 / AI）
- 后端审查：joey（待正式登记）
- 前端负责人：joey；前端审查：angjustinl（待正式登记）
- 产品、测试与交付验收：kirinbao（待真实账号执行）
- 工程交付提交：[`e6a1b61`](https://github.com/qilinbro/msg-manager/commit/e6a1b61)，已直接推送 `main`；提交不替代真实验收。

## 验收范围

本子项把微信本地数据库的可选会话从私聊扩展到群聊。群会话使用稳定的 `@@...` ID，入库和查询统一标记 `conversation_kind=group`。读取仍然是本机只读文本读取，不发送消息、不修改微信数据库、不提取新的密钥。

## 工程检查

| 检查项 | 结果 | 证据 |
|---|---|---|
| `@@...` 群会话 ID 校验与 `private/group` 分类 | 通过 | `internal/connector/wechatdb/reader_test.go`：`TestGroupChatIdentityAndSenderNormalization` |
| 群成员消息方向识别 | 通过 | 群成员 wxid 入站；本人 wxid 出站；未知发送者拒绝 |
| 群会话导入与类型冲突回滚 | 通过 | `internal/store/wechat_test.go`：`TestWeChatGroupConversationKindAndConflict` |
| 存储迁移与旧私聊兼容 | 通过 | `migrations/0011_wechat_group_conversations.sql`、store 迁移回归 |
| API/桌面客户端字段和群聊标签 | 通过 | `internal/api/wechat.go`、`apps/desktop/src/wechat-api.ts`、`WechatPanel.tsx` |
| 客户端合成回归 | 通过 | `node scripts/test-wechat-monitor.mjs` |
| OpenAPI 契约 | 通过 | `node scripts/check-contract.mjs` |

## 真实验收步骤

使用用户本人已授权的 Windows 微信 4.1.15.56 配置，在电脑和后台保持运行：

1. 重启 `scripts/start-inbox.ps1`，确认服务状态可用；不得重新发送数据库密钥。
2. 在微信面板刷新会话列表，选择一个真实群聊，核对列表显示“群聊”和 `@@...` 会话 ID。
3. 手动读取最近文本，检查群成员来信、本人在群内发出的消息方向、时间和正文；核对消息中心的会话 `conversation_kind=group`。
4. 对同一群重复读取，确认只增加重复计数，不生成重复消息；故意改变类型的导入不得覆盖原群会话。
5. 明确把该群加入后台名单，观察至少一个周期的缓存状态和新增消息；关闭页面后确认后台仍按授权运行，暂停后不再读取。
6. 返回消息页，确认群聊可以搜索、查看历史、归档和纳入日报；确认没有发送按钮或自动回复动作。

## 通过标准

- 真实群聊列表、文本读取和 `conversation_kind=group` 归档均成功。
- 至少验证 10 条真实群聊文本，其中包含至少 1 条其他成员入站和 1 条本人出站；内容、方向和时间一致。
- 同一消息重复读取 3 次不产生重复记录；源库变化、未知发送者和非文本记录按现有错误/跳过规则处理。
- 后台周期读取至少连续 2 个周期成功；暂停、关闭和服务停止行为符合只读授权边界。
- joey 完成后端审查、angjustinl 完成前端审查，kirinbao 记录真实验收结论后，才能填写实际完成日期并把子项标记为“已完成”。

## 当前结论

工程检查已通过，真实 Windows 微信群聊库、后台周期读取、指定人员审查和 kirinbao 验收尚未完成。群成员目录、@提及结构化、群权限/管理、媒体本体、历史补齐和发送不在本子项范围内。
