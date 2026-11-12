# Telegram个人账号自动同步候选

- 项目：[gotd/td](https://github.com/gotd/td)。固定main提交`112718adf863b47ffb1ec81aff34955d7d360633`。
- Go MTProto客户端，支持用户和机器人；[MIT许可](https://github.com/gotd/td/blob/112718adf863b47ffb1ec81aff34955d7d360633/LICENSE)。本轮仅阅读，没有加入go.mod或授权真实账号。

用本人账号授权的客户端处理历史及新消息，可以减少Telegram Desktop导出步骤。已有[历史迭代器](https://gotd.dev/docs/helpers/query-iterators/)，支持按消息ID范围查询；重新连接和消息状态处理仍要在本产品持久化层核验。

本人账号需使用自己的`api_id/api_hash`并完成授权，按Telegram提供的机制安全保存会话；这与Bot Token不同。参考[官方应用授权说明](https://core.telegram.org/api/obtaining_api_id)、[历史接口](https://core.telegram.org/method/messages.getHistory)、[更新及缺口处理](https://core.telegram.org/api/updates)。

后续步骤：本机凭据/会话保护→明确选择联系人→历史分页→事务落库与水位→更新状态及缺口补查。优先普通个人云端私聊；群组、频道、秘密聊天、媒体和多账号需另验，不因库宣称功能齐全就提前承诺支持。

采用决定：**Go同栈的后续候选，先完成QQ链路；Bot接口不当作本人全部历史接口。**
