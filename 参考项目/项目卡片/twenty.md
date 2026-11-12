# Twenty

> 核验日期：2026-11-12 · 分类：CRM 与 UI · P1：React 组件与记录交互

## 来源与保存版本

- 仓库：[twentyhq/twenty](https://github.com/twentyhq/twenty)；[项目官网 / 官方资料](https://twenty.com/)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`0b4daad6f27ea5d15146a2fc071ef19476dde16f`](https://github.com/twentyhq/twenty/commit/0b4daad6f27ea5d15146a2fc071ef19476dde16f)。
- 提交时间：2026-11-12T07:14:08Z（上游原时区；不是 pushed_at）。
- 技术栈：React + TypeScript；NestJS / PostgreSQL。
- 许可：[混合许可：主体 AGPLv3 + Application Exception；Enterprise 文件商业许可；twenty-ui 等指定包 MIT](https://github.com/twentyhq/twenty/blob/0b4daad6f27ea5d15146a2fc071ef19476dde16f/LICENSE)。
- 本地源码：[`源码/twenty`](../源码/twenty/)；实际检出 4111 个文件，工作树干净。
- 精简目录：`packages/twenty-ui/src`、`packages/twenty-front/src/modules/object-record`、`packages/twenty-front/src/modules/activities/timeline-activities`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

已有 CRM 记录列表、记录详情、活动时间线和独立组件库。

## 对本项目的借鉴

参考联系人工作台、列表到详情的操作路径、活动记录展示和设计变量；组件复用需逐包确认。

- 对应任务：`T08`、`T14`、`T21`。
- 阅读分工：joey 与前端实现岗位优先研究；吕佳航确认组件边界。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [记录交互模块：`packages/twenty-front/src/modules/object-record`](https://github.com/twentyhq/twenty/tree/0b4daad6f27ea5d15146a2fc071ef19476dde16f/packages/twenty-front/src/modules/object-record)
- [活动时间线：`packages/twenty-front/src/modules/activities/timeline-activities`](https://github.com/twentyhq/twenty/tree/0b4daad6f27ea5d15146a2fc071ef19476dde16f/packages/twenty-front/src/modules/activities/timeline-activities)
- [组件库与独立 LICENSE：`packages/twenty-ui`](https://github.com/twentyhq/twenty/tree/0b4daad6f27ea5d15146a2fc071ef19476dde16f/packages/twenty-ui)

## 限制与采用决定

整个仓库不是单一 MIT 或单一 AGPL。twenty-ui/LICENSE 单独为 MIT，不能把该结论扩展至其他包；本项目仍需独立消息连接器、联系人授权与审批机制。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
