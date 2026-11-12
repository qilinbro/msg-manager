# AstrBot

> 核验日期：2026-11-12 · 分类：多平台 AI 机器人 · P1：平台抽象与会话限频

## 来源与保存版本

- 仓库：[AstrBotDevs/AstrBot](https://github.com/AstrBotDevs/AstrBot)；[项目官网 / 官方资料](https://docs.astrbot.app/)。
- 默认分支：`master`；未归档（核验时）。
- 本地提交：[`2c3ee62b15eb0d329f608481002bed5fc45e84e7`](https://github.com/AstrBotDevs/AstrBot/commit/2c3ee62b15eb0d329f608481002bed5fc45e84e7)。
- 提交时间：2026-11-12T12:51:26+08:00（上游原时区；不是 pushed_at）。
- 技术栈：Python。
- 许可：[AGPL-3.0](https://github.com/AstrBotDevs/AstrBot/blob/2c3ee62b15eb0d329f608481002bed5fc45e84e7/LICENSE)。
- 本地源码：[`源码/astrbot`](../源码/astrbot/)；实际检出 175 个文件，工作树干净。
- 精简目录：`astrbot/core/platform`、`astrbot/core/pipeline`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

已有平台事件抽象、Telegram 适配和消息处理流水线，包含限频阶段。

## 对本项目的借鉴

研究统一消息事件、平台连接状态、会话级并发控制和限流，作为本项目 Connector/Policy 边界参考。

- 对应任务：`T02`、`T06`、`T12`、`T13`、`T17`。
- 阅读分工：吕佳航研究平台抽象、会话锁和频控。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [平台抽象：`astrbot/core/platform/platform.py`](https://github.com/AstrBotDevs/AstrBot/blob/2c3ee62b15eb0d329f608481002bed5fc45e84e7/astrbot/core/platform/platform.py)
- [Telegram 适配器：`astrbot/core/platform/sources/telegram/tg_adapter.py`](https://github.com/AstrBotDevs/AstrBot/blob/2c3ee62b15eb0d329f608481002bed5fc45e84e7/astrbot/core/platform/sources/telegram/tg_adapter.py)
- [限频阶段：`astrbot/core/pipeline/rate_limit_check/stage.py`](https://github.com/AstrBotDevs/AstrBot/blob/2c3ee62b15eb0d329f608481002bed5fc45e84e7/astrbot/core/pipeline/rate_limit_check/stage.py)

## 限制与采用决定

直接复用代码需核对 AGPL 条件；现有插件和适配器不能替代本项目持久化收件/发件、审批与 unknown 状态机。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。

## 本项目采用进展（2026-11-12）

用户要求优先自己的个人账号、以自有产品服务为核心。已阅读本地 aiocqhttp 个人QQ适配器；参考其OneBot边界，在本产品Go服务独立实现HTTP/正向WS只读账号核验和文本转换，不复制AGPL实现、不安装AstrBot作为主服务。AstrBot自身采用反向WS，两个方向配置不同。详见[本项目ADR](../../docs/adr/0003-personal-account-adapters.md)与[接入说明](../../docs/qq-onebot-setup.md)。
