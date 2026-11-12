# Mem0

> 核验日期：2026-11-12 · 分类：长期记忆 · P1：记忆 API 与检索过滤；MVP 保留接口

## 来源与保存版本

- 仓库：[mem0ai/mem0](https://github.com/mem0ai/mem0)；[项目官网 / 官方资料](https://docs.mem0.ai/open-source/overview)。
- 默认分支：`main`；未归档（核验时）。
- 本地提交：[`f1791daa86d469249c9b002f62644d58a1eb96a0`](https://github.com/mem0ai/mem0/commit/f1791daa86d469249c9b002f62644d58a1eb96a0)。
- 提交时间：2026-11-06T22:23:06+05:30（上游原时区；不是 pushed_at）。
- 技术栈：Python；另有 TypeScript SDK。
- 许可：[Apache-2.0](https://github.com/mem0ai/mem0/blob/f1791daa86d469249c9b002f62644d58a1eb96a0/LICENSE)。
- 本地源码：[`源码/mem0`](../源码/mem0/)；实际检出 110 个文件，工作树干净。
- 精简目录：`mem0/memory`、`mem0/configs`、`mem0/vector_stores`；根目录文件和父级文件由 Git 稀疏规则保留。
- 验证层级：文档与源码静态核验；未安装、未运行。

## 已实现内容

已有记忆存储、查询过滤、模型与向量存储适配。

## 对本项目的借鉴

参考用户/会话/Agent 的过滤维度、记忆 API 与存储接口，转化为本项目联系人和会话范围约束。

- 对应任务：`T09`、`T10`、`T11`、`E03`。
- 阅读分工：angjustinl研究记忆作用域与存储抽象。
- 第一步读 README 与 LICENSE；第二步沿下列入口追踪数据流；第三步形成适用于本项目的接口/设计草案，由负责人确认后再选依赖。

## 已核验源码入口

- [记忆实现及过滤构造：`mem0/memory/main.py`](https://github.com/mem0ai/mem0/blob/f1791daa86d469249c9b002f62644d58a1eb96a0/mem0/memory/main.py)
- [记忆存储：`mem0/memory/storage.py`](https://github.com/mem0ai/mem0/blob/f1791daa86d469249c9b002f62644d58a1eb96a0/mem0/memory/storage.py)
- [向量存储适配：`mem0/vector_stores`](https://github.com/mem0ai/mem0/tree/f1791daa86d469249c9b002f62644d58a1eb96a0/mem0/vector_stores)

## 限制与采用决定

user_id 不能直接替代“工作区+连接账号+联系人+会话”的权限边界。README 明示部分基准属于托管平台并含专有优化，不将其分数作为开源 SDK 或本项目实测；首版不因收录此项目就新增向量服务。

当前决定：**作为源码阅读与设计参考，尚未正式选为产品依赖。** 上游能力不能代替本项目真实授权、权限、隔离、故障恢复和验收测试。
