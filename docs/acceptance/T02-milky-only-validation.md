# T02 QQ 个人号仅保留 Milky：验证与待验收记录

- 任务：`T02` / `FEAT-T02`；变更：`CHG-T02-006`。
- 计划实施 / 实际开始：2026-11-17（Asia/Shanghai）；实际完成日期：—。
- 交付分支：`feat/qq-milky-only`，基于已存在提交 `e16fbc5`；不将其他工作区未提交功能纳入本次变更。
- 负责人：angjustinl（后端）、joey（前端）；指定非作者审查：joey（后端）、angjustinl（前端）。实际正式审查：—。
- 产品、测试和交付：kirinbao；真实 Milky 账号 / 浏览器 / Tauri 验收：—。
- 当前状态：移除实现与工程检查通过，子项待审查 / 待验收；T02 整项进行中，MVP 0/27、扩展 0/5 不变。

## 交付与验收指标

| 板块 | 交付 | 验收指标 | 当前证据 |
|---|---|---|---|
| 后台连接 | 移除 OneBot 配置、连接器和核验路由；保留 Milky | 系统 QQ 目录只有 `qq-milky`；旧路由不能核验账号；旧环境变量不发起连接 | API / config / connector 回归通过；旧路由鉴权后 404、匿名仍 401、不可信来源 403 |
| 工作台 | 移除 OneBot 面板、请求函数和占位连接器 | QQ 设置仅显示 Milky；账号/版本核验、同步和后台授权入口保留 | Milky 客户端/组件回归、前端格式/类型/生产构建通过 |
| 契约 | 更新 OpenAPI 与契约检查 | 不再声明 OneBot 路由或专属返回结构；Milky 契约与发送关闭边界不变 | `node scripts/check-contract.mjs` 通过 |
| 文档与配置 | 更新环境示例、架构、任务 / feat 和迁移说明 | 当前操作只引导 Milky；历史资料标明已退役，不改写历史交付 | 文档同步、差异检查通过 |
| 数据与权限 | 不新增数据迁移、不删除聊天或联系人 | 已有加密库可用，原有 Milky 授权和发送关闭不受影响 | 合成 Milky 临时加密库同步/去重/名单恢复及发送关闭集成通过；本人数据未验收 |

工程验证仅使用合成协议端、临时加密库和测试凭据；真实个人 QQ 登录、真实消息及指定人员批准不能由模拟结果代替。

## 工程检查记录（2026-11-17）

独立分支工作区 `D:\msg_manager\.runtime\qq-milky-only-20261117`：

- 完整 `scripts/check.ps1` 通过：Go 测试 / vet（含 Milky 集成）、OpenAPI 契约、PowerShell bootstrap 9 个子场景及启动器 53 次隔离启动、全部客户端回归、session 11 组 / device 31 组、前端格式 / 类型 / Vite 构建。JS 406.67 kB、CSS 47.93 kB。
- 后端 API / config / connector 定向 race 检查通过。目录、旧配置忽略、旧路由退休与鉴权边界的专项证据分别见 `internal/connector/connector_test.go`、`internal/config/config_test.go`、`internal/api/milky_only_test.go`、`tests/integration/qq_retirement_test.go`。
- `tests/integration/qq_milky_test.go` 保留账号核验、鉴权、授权白名单、重启恢复、历史入库去重和发送关闭回归。`scripts/test-milky-client.mjs` 覆盖设置入口仅 Milky、原 OneBot 面板不渲染以及已有 Milky 交互。
- 先按仓库 checkout 规则规范化 CRLF 后完整检查通过；换行处理没有改变功能或验收结论。

当前运行目录 `D:\msg_manager` 仅选择性同步本次 OneBot 移除，保留该目录原有较早鉴权基线，未用独立分支整树覆盖：API / config / connector / integration、Milky 客户端、11 组会话、前端类型及构建通过（JS 391.14 kB、CSS 47.30 kB）。两份构建来自不同基线，不视为完全相同的交付包。

2026-11-17 已重启根运行目录后台并进行本人授权本机服务的元数据检查：鉴权 `GET /api/v1/system` 仅返回一个 QQ 连接器 `qq-milky`，旧核验接口鉴权后返回 404，工作台 HTTP 200，加密存储配置可用，自动发送关闭。当前 Milky 为 `not_configured`；未登录或读取真实 QQ 消息，也未验收真实界面。启动器沿用该目录原有令牌方式，新服务令牌已复制到本机剪贴板，未输出到验证记录。独立交付分支采用已提交的持久凭据基线，二者区别保留如上。

辅助工程复核不能代替 joey / angjustinl 正式审查。真实 Milky 账号、本人消息和原生 Tauri 尚未验收；实际完成日期 —，本轮在 `feat/qq-milky-only` 提交推送，不创建 PR，也不合并其他功能到 main。新提交生成前不填写哈希。

## 真实验收步骤

1. 按 [Milky 接入说明](../qq-milky-setup.md)配置本人协议端并启动更新后的后台。
2. 确认「设置」只显示一个 QQ Milky 入口，连接目录不再显示 OneBot，旧 OneBot 核验地址不再可用。
3. 核验 Milky 账号及 1.3 版本；对授权好友同步历史，核对消息中心已有记录未丢失、重复导入去重。
4. 重新确认后台接收白名单，验证接收、重启后待重新核验状态、停止和缺口提示；确认发送保持关闭。
5. 登记 joey 后端、angjustinl 前端正式审查和 kirinbao 验收证据，再决定实际完成日期。T02 整项收发验收仍依赖后续规则、审批和发件箱。
