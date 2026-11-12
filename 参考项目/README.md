# Social Intelligence OS 参考项目

> 核验日期：2026-11-12（Asia/Shanghai）  
> 关联：FEAT-T01 / CHG-T01-002 · 只作为调研资料，不代表任何开发任务验收完成。

已从 GitHub、项目官网与实际代码筛选 **12 个公开实现项目**，并在本地保存对应分支的精简源码副本。目录内同时包含开放源码许可项目和受限源码许可项目，不能笼统视为可自由商用。

## 先看哪些

- **整体 AI 消息处理：** [LangBot](项目卡片/langbot.md)，再对照 [AstrBot](项目卡片/astrbot.md) 的平台与限频抽象。
- **联系人与关系档案：** [Monica](项目卡片/monica.md) 的信息模型；[Bonds](项目卡片/bonds.md) 的 Go + React 结构很贴近，但 BSL 许可限制必须单独确认。
- **joey 的前端 / UI 阅读：** [Chatwoot](项目卡片/chatwoot.md) 的消息与联系人侧栏、[Twenty](项目卡片/twenty.md) 的记录与组件、Bonds 的联系人详情。
- **吕佳航的首个连接器：** [go-telegram/bot](项目卡片/go-telegram-bot.md)，特别看游标与落库先后，不能直接沿用默认轮询循环。
- **长期记忆：** [Mem0](项目卡片/mem0.md)；事实时间与关系冲突的后续方案看 [Graphiti](项目卡片/graphiti.md)。
- **常驻运行：** [OpenClaw](项目卡片/openclaw.md)、[Letta Code](项目卡片/letta-code.md)；任务恢复与重试结构补看 [Temporal](项目卡片/temporal.md)。

以上优先级是结合本项目技术栈、MVP 周期和许可作出的借鉴建议，不是对上游可靠性的认证。

## 项目总览

| 项目 | 已实现方向 | 技术栈 | 许可概要 | 主要对应任务 |
|---|---|---|---|---|
| [Bonds](项目卡片/bonds.md) | 联系人与关系管理 | Go + React + TypeScript + SQLite | Business Source License 1.1（BSL，非开放源码许可证） | T05、T07、T14、E02 |
| [Monica](项目卡片/monica.md) | 联系人与关系管理 | PHP / Laravel + Vue | AGPL-3.0 | T07、T09、T14 |
| [Twenty](项目卡片/twenty.md) | CRM 与 UI | React + TypeScript；NestJS / PostgreSQL | 混合许可：主体 AGPLv3 + Application Exception；Enterprise 文件商业许可；twenty-ui 等指定包 MIT | T08、T14、T21 |
| [Chatwoot](项目卡片/chatwoot.md) | 统一消息与 UI | Ruby on Rails + Vue | 普通目录 MIT Expat；enterprise/ 独立许可；第三方各自许可 | T06、T07、T08、T17、T21 |
| [LangBot](项目卡片/langbot.md) | 多平台 AI 机器人 | Python；多平台适配与 Web 管理 | Apache-2.0 | T02、T06、T09、T10、T13、T15、T17 |
| [AstrBot](项目卡片/astrbot.md) | 多平台 AI 机器人 | Python | AGPL-3.0 | T02、T06、T12、T13、T17 |
| [go-telegram/bot](项目卡片/go-telegram-bot.md) | Go 平台连接 | Go | MIT | T02、T06、T17 |
| [Mem0](项目卡片/mem0.md) | 长期记忆 | Python；另有 TypeScript SDK | Apache-2.0 | T09、T10、T11、E03 |
| [Graphiti](项目卡片/graphiti.md) | 关系与时间记忆 | Python；图数据库适配 | Apache-2.0 | T09、T10、E03 |
| [OpenClaw](项目卡片/openclaw.md) | 常驻个人 Agent | TypeScript / Node.js | MIT | T12、T15、T16、T19、T20、T21 |
| [Letta Code](项目卡片/letta-code.md) | 有状态 Agent | TypeScript | Apache-2.0 | T11、T12、T15、T16、T19 |
| [Temporal](项目卡片/temporal.md) | 持久化执行 | Go 服务 | MIT | T15、T19、T23、T26 |

## 文件结构

```text
参考项目/
├─ README.md                 # 总览与阅读顺序
├─ 借鉴路线.md               # 按任务和人员分配阅读目标
├─ 核验与维护说明.md         # 核验口径、许可、源码恢复和限制
├─ 项目清单.json             # 固定提交、真实文件入口、许可与下载范围
├─ 项目卡片/                # 每项目独立说明与固定提交链接，共 12 份
└─ 源码/                    # 本地精简克隆，12 个独立上游仓库，合计约 406 MiB（含 Git 数据）
```

## 使用边界

- 每份卡片记录实际保存的提交、源码入口、已实现内容、可借鉴点与限制。
- 本次验证了仓库、许可证、关键路径和本地源码可读性；**没有安装依赖、启动服务或验证运行效果**。
- 精简副本包含根目录文件、许可证及指定源码目录，不是完整可构建交付物。扩展检出方法见 [核验与维护说明](核验与维护说明.md)。
- 上游平台支持列表不等同于本项目已获个人 QQ/微信接入权限；本项目的审批、联系人隔离、持久化和 unknown 规则仍需自行实现。
- 参考资料与清单同步项目仓库；上游源码保留本地，不重复上传为本项目自有代码。换机后按清单重新获取。
- 详见 [按任务借鉴路线](借鉴路线.md) 和 [机器可读项目清单](项目清单.json)。
