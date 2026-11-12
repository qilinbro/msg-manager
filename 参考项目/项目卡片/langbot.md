# LangBot

> 核验日期：2026-11-12 · 分类：多平台 AI 机器人 · P0：整体能力拆分优先参考

## 来源与保存版本

- 仓库：[langbot-app/LangBot](https://github.com/langbot-app/LangBot)；[项目官网 / 官方资料](https://langbot.app/docs/en/insight/features/)。
- 默认分支：`master`；未归档（核验时）。
- 本地提交：[`9e31f971e5a2500d605f731928d85aa7f5fc867f`](https://github.com/langbot-app/LangBot/commit/9e31f971e5a2500d605f731928d85aa7f5fc867f)。
- 提交时间：2026-11-12T15:47:19+08:00（上游原时区；不是 pushed_at）。
- 技术栈：Python；多平台适配与 Web 管理。
- 许可：[Apache-2.0](https://github.com/langbot-app/LangBot/blob/9e31f971e5a2500d605f731928d85aa7f5fc867f/LICENSE)。
- 本地源码：[`源码/langbot`](../源码/langbot/)；实际检出 426 个文件，工作树干净。
- 精简目录：`src/langbot/pkg/platform`、`src/langbot/pkg/pipeline`、`plugins/KnowledgeEngine/LongTermMemory`、`plugins/misc/HumanTakeover`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

已有多平台机器人适配、模型接入、消息处理流水线；当前源码包含长期记忆及人工接管插件。

## 对本项目的借鉴

参考 Telegram adapter、pipeline controller、记忆插件与人工接管的模块边界，映射到 Go 连接器和任务模块。

- 对应任务：`T02`、`T06`、`T09`、`T10`、`T13`、`T15`、`T17`。
- 阅读分工：angjustinl优先研究适配器、处理流水线与插件边界。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [Telegram 适配器：`src/langbot/pkg/platform/adapters/telegram/adapter.py`](https://github.com/langbot-app/LangBot/blob/9e31f971e5a2500d605f731928d85aa7f5fc867f/src/langbot/pkg/platform/adapters/telegram/adapter.py)
- [处理流水线：`src/langbot/pkg/pipeline/controller.py`](https://github.com/langbot-app/LangBot/blob/9e31f971e5a2500d605f731928d85aa7f5fc867f/src/langbot/pkg/pipeline/controller.py)
- [长期记忆插件：`plugins/KnowledgeEngine/LongTermMemory/components/knowledge_engine/memory_engine.py`](https://github.com/langbot-app/LangBot/blob/9e31f971e5a2500d605f731928d85aa7f5fc867f/plugins/KnowledgeEngine/LongTermMemory/components/knowledge_engine/memory_engine.py)
- [人工接管插件：`plugins/misc/HumanTakeover`](https://github.com/langbot-app/LangBot/tree/9e31f971e5a2500d605f731928d85aa7f5fc867f/plugins/misc/HumanTakeover)

## 限制与采用决定

Python 项目不能直接作为 Go 包接入；HumanTakeover 不自动满足不可变审批快照。支持某平台不等于个人 QQ/微信具备官方发送授权，平台能力须另验。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
