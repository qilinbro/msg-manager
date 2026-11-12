# Bonds

> 核验日期：2026-11-12 · 分类：联系人与关系管理 · P1：概念与同栈结构，复制前先确认许可

## 来源与保存版本

- 仓库：[naiba/bonds](https://github.com/naiba/bonds)；[项目官网 / 官方资料](https://naiba.github.io/bonds/)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`69df488cb55885097612d9908670058d75bf2b8d`](https://github.com/naiba/bonds/commit/69df488cb55885097612d9908670058d75bf2b8d)。
- 提交时间：2026-11-08T14:36:23+08:00（上游原时区；不是 pushed_at）。
- 技术栈：Go + React + TypeScript + SQLite。
- 许可：[Business Source License 1.1（BSL，非开放源码许可证）](https://github.com/naiba/bonds/blob/69df488cb55885097612d9908670058d75bf2b8d/LICENSE)。
- 本地源码：[`源码/bonds`](../源码/bonds/)；实际检出 1106 个文件，工作树干净。
- 精简目录：`server/internal`、`web/src`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

提供联系人、笔记、提醒、关系记录、资料分区和 Web 页面；项目栈与本方案接近。

## 对本项目的借鉴

研究联系人模型、资料模块化、删除/合并边界以及 Go 服务与 React 页面之间的契约。

- 对应任务：`T05`、`T07`、`T14`、`E02`。
- 阅读分工：angjustinl阅读数据与服务；joey 阅读联系人详情。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [联系人模型：`server/internal/models/contact.go`](https://github.com/naiba/bonds/blob/69df488cb55885097612d9908670058d75bf2b8d/server/internal/models/contact.go)
- [联系人合并服务：`server/internal/services/contact_merge.go`](https://github.com/naiba/bonds/blob/69df488cb55885097612d9908670058d75bf2b8d/server/internal/services/contact_merge.go)
- [联系人详情 UI：`web/src/pages/contact/ContactDetail.tsx`](https://github.com/naiba/bonds/blob/69df488cb55885097612d9908670058d75bf2b8d/web/src/pages/contact/ContactDetail.tsx)

## 限制与采用决定

根 LICENSE 是 BSL 1.1，并有商业使用、托管及再许可限制，不能按 MIT 项目处理；当前只作阅读参考，不将代码并入本产品。它也不能证明已实现本项目的平台自动回复授权。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
