# QQ Chat Exporter：QQ历史自动读取候选

- 上游：[shuakami/qq-chat-exporter](https://github.com/shuakami/qq-chat-exporter)。
- 固定提交：`07113d8ebb2be829db9b9c58865a43629cad1a57`；分支master。
- 许可：[GPL-3.0](https://github.com/shuakami/qq-chat-exporter/blob/07113d8ebb2be829db9b9c58865a43629cad1a57/LICENSE)。本轮不复制实现代码进入产品，不安装或分发其运行包。
- 验证层级：README、接口与任务源码静态核验；没有真实QQ测试。

## 为什么适合减少手工

QCE具备本机HTTP历史查询接口。本产品可以在用户选定联系人后，后台读取并映射到统一消息结构；用户不必导出文件再改字段。普通历史查询足以作为首轮适配目标，定时导出只作任务设计参考。

QCE依赖已登录的QQ/NapCat运行环境。它的HTTP接口不是OneBot接口，`QQ_ONEBOT_URL`不能直接用作QCE地址。独立查看模式没有在线bridge，不能用于获取QQ历史。

## 已核对接口

1. `GET /api/system/info`：正向要求`success=true`、`data.mode=plugin`、`data.napcat.online=true`、非空`selfInfo.uid`，并核对`selfInfo.uin`等于用户绑定的QQ号。该接口会把部分bridge失败转为空信息，HTTP200或success不能单独证明登录成功。[源码](https://github.com/shuakami/qq-chat-exporter/blob/07113d8ebb2be829db9b9c58865a43629cad1a57/qq-chat-export-server/src/api/routes/system.rs#L201)。
2. `POST /api/messages/fetch`：`peer.chatType=1`、`peer.peerUid`为实际联系人UID；`page`从1开始、`limit`默认50上限2000，`filter.startTime/endTime`是毫秒。返回消息、分页和缓存状态。首轮在本产品中另设更小的总量与页数上限。[源码](https://github.com/shuakami/qq-chat-exporter/blob/07113d8ebb2be829db9b9c58865a43629cad1a57/qq-chat-export-server/src/api/routes/messages.rs#L598)。
3. `POST /api/scheduled-exports`：持久化定时导出配置与执行记录，但输出仍是文件，没有本产品所需的可靠消息游标。相对时间范围是秒偏移，不可与fetch接口毫秒时间混用。[路由](https://github.com/shuakami/qq-chat-exporter/blob/07113d8ebb2be829db9b9c58865a43629cad1a57/qq-chat-export-server/src/api/routes/scheduled.rs)、[调度器](https://github.com/shuakami/qq-chat-exporter/blob/07113d8ebb2be829db9b9c58865a43629cad1a57/qq-chat-export-server/src/scheduler/manager.rs)。
4. 实验性漫游任务提供有界日期扫描和partial结果，使用Unix秒，不能证明腾讯服务端记录完整；不作为首版默认全历史承诺。[上游文档](https://github.com/shuakami/qq-chat-exporter/blob/07113d8ebb2be829db9b9c58865a43629cad1a57/docs/roaming-api.md)。

认证使用本机配置的Bearer令牌；不放进URL、不提交给模型。产品应只允许固定的本机地址和查询端点，不暴露任意RPC或导出目录控制。

## 标识和一致性

原始消息保留`msgId/msgSeq/msgTime/senderUin/peerUid`；大整数始终按精确字符串处理。建议以本人账号、会话类型、对方UID、原始msgId建立身份，不能将QCE原始msgId等同OneBot的短message_id。

QCE固定引用的旧NapCat实现曾把原始消息/会话映射为31位短ID并在内存缓存；据此不能作跨来源永久去重键。实时与历史来源没有可靠关联信息时，保留来源并标待关联，不用昵称、正文或时间强行合并。

同步使用固定时间窗、重叠补查和自有SQLCipher水位；分页或写入失败不推进完成水位。历史范围由当前账号和客户端实际可见数据决定，不能承诺全历史无遗漏。新消息接收不代替断线补查。

## 上游与许可限制

NapCat当前主仓仅剩结束说明；QCE内引用的历史NapCat源码为`5bfbf92c215f2c83d9b672a1b296ca2ce37c055d`，不表示用户实例已验证。该版本许可存在用途及再分发限制，不能当宽松许可直接复制；外部API接入也不自动代表获得所有许可或平台授权。接入已有实例、部署新版本和产品分发分别核对。

采用决定：**优先实现独立只读QCE适配器，先做身份核验和一个联系人历史同步，再接持久化自动增量；尚未实现或部署。**
