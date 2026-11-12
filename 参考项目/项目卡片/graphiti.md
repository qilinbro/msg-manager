# Graphiti

> 核验日期：2026-11-12 · 分类：关系与时间记忆 · P2：第二阶段事实时间与关系模型

## 来源与保存版本

- 仓库：[getzep/graphiti](https://github.com/getzep/graphiti)；[项目官网 / 官方资料](https://github.com/getzep/graphiti)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`ebef986a0b2360211b65751c111cacb4a7d790eb`](https://github.com/getzep/graphiti/commit/ebef986a0b2360211b65751c111cacb4a7d790eb)。
- 提交时间：2026-11-11T01:25:27Z（上游原时区；不是 pushed_at）。
- 技术栈：Python；图数据库适配。
- 许可：[Apache-2.0](https://github.com/getzep/graphiti/blob/ebef986a0b2360211b65751c111cacb4a7d790eb/LICENSE)。
- 本地源码：[`源码/graphiti`](../源码/graphiti/)；实际检出 188 个文件，工作树干净。
- 精简目录：`graphiti_core`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

实现基于事件的知识图谱维护；边模型包含分组和时间有效性字段，配有检索模块。

## 对本项目的借鉴

参考事件证据、关系边、事实生效/失效时间，处理“以前喜欢某物、后来改变”的记忆冲突。

- 对应任务：`T09`、`T10`、`E03`。
- 阅读分工：吕佳航研究事实有效期和证据建模。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [事件与图更新入口：`graphiti_core/graphiti.py`](https://github.com/getzep/graphiti/blob/ebef986a0b2360211b65751c111cacb4a7d790eb/graphiti_core/graphiti.py)
- [关系边与有效时间：`graphiti_core/edges.py`](https://github.com/getzep/graphiti/blob/ebef986a0b2360211b65751c111cacb4a7d790eb/graphiti_core/edges.py)
- [检索过滤：`graphiti_core/search/search_filters.py`](https://github.com/getzep/graphiti/blob/ebef986a0b2360211b65751c111cacb4a7d790eb/graphiti_core/search/search_filters.py)

## 限制与采用决定

图存储及模型依赖超出当前 SQLite MVP。group_id 属于数据分区手段，不自动等同于本项目的授权校验；不直接照搬其数据库部署。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
