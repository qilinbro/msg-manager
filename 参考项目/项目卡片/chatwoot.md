# Chatwoot

> 核验日期：2026-11-12 · 分类：统一消息与 UI · P1：消息收件箱与联系人侧栏

## 来源与保存版本

- 仓库：[chatwoot/chatwoot](https://github.com/chatwoot/chatwoot)；[项目官网 / 官方资料](https://www.chatwoot.com/)。
- 默认分支：`develop`；未归档（核验时）。
- 本地提交：[`bf541372d1e75d7b65a17169e7057a24e7af44ad`](https://github.com/chatwoot/chatwoot/commit/bf541372d1e75d7b65a17169e7057a24e7af44ad)。
- 提交时间：2026-11-12T12:41:17+05:30（上游原时区；不是 pushed_at）。
- 技术栈：Ruby on Rails + Vue。
- 许可：[普通目录 MIT Expat；enterprise/ 独立许可；第三方各自许可](https://github.com/chatwoot/chatwoot/blob/bf541372d1e75d7b65a17169e7057a24e7af44ad/LICENSE)。
- 本地源码：[`源码/chatwoot`](../源码/chatwoot/)；实际检出 333 个文件，工作树干净。
- 精简目录：`app/models`、`app/services/telegram`、`app/jobs`、`app/javascript/dashboard/routes/dashboard/conversation`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

实现多渠道客服收件箱、联系人/会话关联、渠道消息接收与回复任务。

## 对本项目的借鉴

参考消息列表、会话区和联系人侧栏的结构，以及平台事件到内部会话、发送任务的映射。

- 对应任务：`T06`、`T07`、`T08`、`T17`、`T21`。
- 阅读分工：joey 先看会话与侧栏；angjustinl看消息归属和发送任务。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [Telegram 入站消息：`app/services/telegram/incoming_message_service.rb`](https://github.com/chatwoot/chatwoot/blob/bf541372d1e75d7b65a17169e7057a24e7af44ad/app/services/telegram/incoming_message_service.rb)
- [发送任务：`app/jobs/send_reply_job.rb`](https://github.com/chatwoot/chatwoot/blob/bf541372d1e75d7b65a17169e7057a24e7af44ad/app/jobs/send_reply_job.rb)
- [会话界面：`app/javascript/dashboard/routes/dashboard/conversation/ConversationView.vue`](https://github.com/chatwoot/chatwoot/blob/bf541372d1e75d7b65a17169e7057a24e7af44ad/app/javascript/dashboard/routes/dashboard/conversation/ConversationView.vue)
- [联系人侧栏：`app/javascript/dashboard/routes/dashboard/conversation/ContactPanel.vue`](https://github.com/chatwoot/chatwoot/blob/bf541372d1e75d7b65a17169e7057a24e7af44ad/app/javascript/dashboard/routes/dashboard/conversation/ContactPanel.vue)

## 限制与采用决定

客服场景与个人关系不同；本次保存 develop，不代表稳定发布版本。根 LICENSE 对 enterprise/ 有明确例外，不能称整个项目统一 MIT。其人工服务流程不等于本项目的审批快照和 unknown 发送恢复。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
