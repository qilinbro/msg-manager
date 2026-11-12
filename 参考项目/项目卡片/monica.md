# Monica

> 核验日期：2026-11-12 · 分类：联系人与关系管理 · P1：个人关系信息架构

## 来源与保存版本

- 仓库：[monicahq/monica](https://github.com/monicahq/monica)；[项目官网 / 官方资料](https://www.monicahq.com/)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`dab74273acc5c9e0ba713a9f7c2c71c24ab16993`](https://github.com/monicahq/monica/commit/dab74273acc5c9e0ba713a9f7c2c71c24ab16993)。
- 提交时间：2026-10-28T23:30:10+01:00（上游原时区；不是 pushed_at）。
- 技术栈：PHP / Laravel + Vue。
- 许可：[AGPL-3.0](https://github.com/monicahq/monica/blob/dab74273acc5c9e0ba713a9f7c2c71c24ab16993/LICENSE.md)。
- 本地源码：[`源码/monica`](../源码/monica/)；实际检出 125 个文件，工作树干净。
- 精简目录：`app/Models`、`app/Services`、`resources/js/Pages/Vault/Contact`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

已实现个人联系人档案、关系记录、提醒与时间线，适合校准“好友专属档案”的产品信息结构。

## 对本项目的借鉴

参考联系人、时间线事件、活动和提醒的组织方式，避免把好友档案做成一段不可追溯的长摘要。

- 对应任务：`T07`、`T09`、`T14`。
- 阅读分工：吕佳航研究关系实体；joey 研究档案分区与时间线。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [联系人模型：`app/Models/Contact.php`](https://github.com/monicahq/monica/blob/dab74273acc5c9e0ba713a9f7c2c71c24ab16993/app/Models/Contact.php)
- [时间线事件：`app/Models/TimelineEvent.php`](https://github.com/monicahq/monica/blob/dab74273acc5c9e0ba713a9f7c2c71c24ab16993/app/Models/TimelineEvent.php)
- [联系人界面：`resources/js/Pages/Vault/Contact`](https://github.com/monicahq/monica/tree/dab74273acc5c9e0ba713a9f7c2c71c24ab16993/resources/js/Pages/Vault/Contact)

## 限制与采用决定

本次保存 main，README 标注 beta，稳定版本应另看 4.x；不是已完成的社交 AI 代发系统，不能据此宣称有内置 AI 或个人号接管。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
