# go-telegram/bot

> 核验日期：2026-11-12 · 分类：Go 平台连接 · P0：首个 Go 连接器候选，接收语义必须改造

## 来源与保存版本

- 仓库：[go-telegram/bot](https://github.com/go-telegram/bot)；[项目官网 / 官方资料](https://core.telegram.org/bots/api)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`f061396531935982a05fcb1e09d2e5e7c2742a7c`](https://github.com/go-telegram/bot/commit/f061396531935982a05fcb1e09d2e5e7c2742a7c)。
- 提交时间：2026-11-09T20:12:14+03:00（上游原时区；不是 pushed_at）。
- 技术栈：Go。
- 许可：[MIT](https://github.com/go-telegram/bot/blob/f061396531935982a05fcb1e09d2e5e7c2742a7c/LICENSE)。
- 本地源码：[`源码/go-telegram-bot`](../源码/go-telegram-bot/)；实际检出 155 个文件，工作树干净。
- 精简目录：`models`、`examples`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

Go Telegram Bot API 客户端与框架，含轮询、请求方法、模型和示例。

## 对本项目的借鉴

可评估模型结构、发送 API、取消机制、429 处理；用自己的连接器隔离库接口。

- 对应任务：`T02`、`T06`、`T17`。
- 阅读分工：吕佳航优先阅读接收循环及请求错误处理。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [内存游标与接收循环：`get_updates.go`](https://github.com/go-telegram/bot/blob/f061396531935982a05fcb1e09d2e5e7c2742a7c/get_updates.go)
- [HTTP 请求和错误处理：`raw_request.go`](https://github.com/go-telegram/bot/blob/f061396531935982a05fcb1e09d2e5e7c2742a7c/raw_request.go)
- [处理函数调度：`process_update.go`](https://github.com/go-telegram/bot/blob/f061396531935982a05fcb1e09d2e5e7c2742a7c/process_update.go)
- [使用示例：`examples`](https://github.com/go-telegram/bot/tree/f061396531935982a05fcb1e09d2e5e7c2742a7c/examples)

## 限制与采用决定

已核验 get_updates.go 在将更新交给处理队列前写 lastUpdateID，默认 Start 不等待业务事务提交；下一次轮询可能提前确认事件。MVP 应自行控制持久化后推进的长轮询，或验证可靠接收扩展；只增加 handler 落库并不足够。它是社区库，不是 Telegram 官方 SDK。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
