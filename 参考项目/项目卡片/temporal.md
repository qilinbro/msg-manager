# Temporal

> 核验日期：2026-11-12 · 分类：持久化执行 · P2：任务恢复设计参考，不作为 MVP 新依赖

## 来源与保存版本

- 仓库：[temporalio/temporal](https://github.com/temporalio/temporal)；[项目官网 / 官方资料](https://docs.temporal.io/)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`03ecd1d228d2b8d9d433da55785afc9aeeead918`](https://github.com/temporalio/temporal/commit/03ecd1d228d2b8d9d433da55785afc9aeeead918)。
- 提交时间：2026-11-11T03:39:18Z（上游原时区；不是 pushed_at）。
- 技术栈：Go 服务。
- 许可：[MIT](https://github.com/temporalio/temporal/blob/03ecd1d228d2b8d9d433da55785afc9aeeead918/LICENSE)。
- 本地源码：[`源码/temporal`](../源码/temporal/)；实际检出 209 个文件，工作树干净。
- 精简目录：`common/backoff`、`common/tasks`、`service/history/queues`、`docs/architecture`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

已实现持久化工作流执行服务；源码有退避、任务和历史队列等模块。

## 对本项目的借鉴

参考有界重试、队列状态及恢复的工程分层，再落实到 MVP 的 SQLite jobs 和 lease。

- 对应任务：`T15`、`T19`、`T23`、`T26`。
- 阅读分工：angjustinl研究重试、任务状态与故障测试。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [退避机制：`common/backoff`](https://github.com/temporalio/temporal/tree/03ecd1d228d2b8d9d433da55785afc9aeeead918/common/backoff)
- [任务抽象：`common/tasks`](https://github.com/temporalio/temporal/tree/03ecd1d228d2b8d9d433da55785afc9aeeead918/common/tasks)
- [历史任务队列：`service/history/queues`](https://github.com/temporalio/temporal/tree/03ecd1d228d2b8d9d433da55785afc9aeeead918/service/history/queues)
- [架构文档：`docs/architecture`](https://github.com/temporalio/temporal/tree/03ecd1d228d2b8d9d433da55785afc9aeeead918/docs/architecture)

## 限制与采用决定

本仓库是 Temporal Server，不是应用 Go SDK 或示例项目；不将其完整基础设施引入四周 MVP。工作流可恢复也不能保证第三方消息接口端到端恰好一次。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
