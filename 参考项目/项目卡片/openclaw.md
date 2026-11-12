# OpenClaw

> 核验日期：2026-11-12 · 分类：常驻个人 Agent · P1：后台网关、定时任务及授权交互

## 来源与保存版本

- 仓库：[openclaw/openclaw](https://github.com/openclaw/openclaw)；[项目官网 / 官方资料](https://docs.openclaw.ai/)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`9fe59d5e7ef063a15c39523386c15c0b68b733f3`](https://github.com/openclaw/openclaw/commit/9fe59d5e7ef063a15c39523386c15c0b68b733f3)。
- 提交时间：2026-11-12T16:26:31+08:00（上游原时区；不是 pushed_at）。
- 技术栈：TypeScript / Node.js。
- 许可：[MIT](https://github.com/openclaw/openclaw/blob/9fe59d5e7ef063a15c39523386c15c0b68b733f3/LICENSE)。
- 本地源码：[`源码/openclaw`](../源码/openclaw/)；实际检出 14200 个文件，工作树干净。
- 精简目录：`src/gateway`、`src/memory`、`src/cron`、`src/daemon`、`src/pairing`、`src/security`、`ui/src`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

公开源码包含 gateway、daemon、cron、memory、pairing、security 和管理 UI。

## 对本项目的借鉴

研究长期运行进程、会话状态、计划任务、设备配对与权限交互的职责划分。

- 对应任务：`T12`、`T15`、`T16`、`T19`、`T20`、`T21`。
- 阅读分工：吕佳航研究常驻服务与边界；joey 可研究运行状态信息。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [常驻网关：`src/gateway`](https://github.com/openclaw/openclaw/tree/9fe59d5e7ef063a15c39523386c15c0b68b733f3/src/gateway)
- [定时任务：`src/cron`](https://github.com/openclaw/openclaw/tree/9fe59d5e7ef063a15c39523386c15c0b68b733f3/src/cron)
- [记忆模块：`src/memory`](https://github.com/openclaw/openclaw/tree/9fe59d5e7ef063a15c39523386c15c0b68b733f3/src/memory)
- [配对：`src/pairing`](https://github.com/openclaw/openclaw/tree/9fe59d5e7ef063a15c39523386c15c0b68b733f3/src/pairing)
- [管理界面：`ui/src`](https://github.com/openclaw/openclaw/tree/9fe59d5e7ef063a15c39523386c15c0b68b733f3/ui/src)

## 限制与采用决定

能力范围和代码规模远超本 MVP，不整体引入工具执行或权限体系；消息代发仍需本项目独立审批与发件箱。MIT 根许可不覆盖第三方依赖的全部义务。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
