# Letta Code

> 核验日期：2026-11-12 · 分类：有状态 Agent · P2：Agent 服务与审批、队列结构

## 来源与保存版本

- 仓库：[letta-ai/letta-code](https://github.com/letta-ai/letta-code)；[项目官网 / 官方资料](https://docs.letta.com/letta-code/channels)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`ed08ddcb8e109f5e64076073294a832955ffc23e`](https://github.com/letta-ai/letta-code/commit/ed08ddcb8e109f5e64076073294a832955ffc23e)。
- 提交时间：2026-11-11T22:55:46-08:00（上游原时区；不是 pushed_at）。
- 技术栈：TypeScript。
- 许可：[Apache-2.0](https://github.com/letta-ai/letta-code/blob/ed08ddcb8e109f5e64076073294a832955ffc23e/LICENSE)。
- 本地源码：[`源码/letta-code`](../源码/letta-code/)；实际检出 2693 个文件，工作树干净。
- 精简目录：`src`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

当前源码包含 Agent、后端服务、channels、permissions、queue、cron 等模块。

## 对本项目的借鉴

研究 Agent 运行、渠道入口、权限交互和任务队列的拆分，保持模型与实际执行权限分离。

- 对应任务：`T11`、`T12`、`T15`、`T16`、`T19`。
- 阅读分工：吕佳航研究服务、权限和渠道边界。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [Agent 模块：`src/agent`](https://github.com/letta-ai/letta-code/tree/ed08ddcb8e109f5e64076073294a832955ffc23e/src/agent)
- [权限模块：`src/permissions`](https://github.com/letta-ai/letta-code/tree/ed08ddcb8e109f5e64076073294a832955ffc23e/src/permissions)
- [渠道模块：`src/channels`](https://github.com/letta-ai/letta-code/tree/ed08ddcb8e109f5e64076073294a832955ffc23e/src/channels)
- [队列模块：`src/queue`](https://github.com/letta-ai/letta-code/tree/ed08ddcb8e109f5e64076073294a832955ffc23e/src/queue)

## 限制与采用决定

letta-ai/letta 默认分支当前为文档入口，README 指向此仓库；旧 API 服务在 archive 分支。这里保存的是新源码，不沿用旧版 Python 服务的目录说明。通用工具权限不是联系人自动发送授权。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
