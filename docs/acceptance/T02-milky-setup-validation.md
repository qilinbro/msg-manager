# T02 Milky 安装与配置流程验证

- 变更：`FEAT-T02 / CHG-T02-007`；计划实施、实际开始及工程记录日期：2026-11-17（Asia/Shanghai）。实际完成日期：—。
- 后端 angjustinl，指定后端/脚本审查 joey；前端 joey，指定前端审查 angjustinl；kirinbao 组织验收。正式审查 / 真实验收：—。
- 分支：`feat/qq-milky-only`；实现与工程检查通过，子项待审查 / 待验收，T02 整项进行中。

| 验收指标 | 交付 / 证据 | 结果 |
|---|---|---|
| 从零可找到 Windows 协议端及正确端口 | `docs/qq-milky-setup.md`；官方 Desktop v3.1.2 / LLBot Milky 1.3 源码核验 | 官方来源已核对；实际安装/QQ 登录待用户操作 |
| 设置页填写、测试、核对后保存；编辑后重测 | `MilkySetupPanel.tsx`、`milky-api.ts`、`test-milky-client.mjs` | 专项合成回归通过 |
| GET 无密钥、测试不改状态、保存零联网、重启恢复 | `internal/api/milky_config_test.go` | API 包检查通过；仅合成服务与临时库 |
| 版本冲突、令牌限域、更换账号不复活权限 | `internal/store/qq_milky_config_test.go`、API 用例 | Store/API 包检查通过 |
| 加密保存、不出现明文令牌、错密钥拒绝 | Store 加密文件扫描、重开与凭据输出测试 | 包检查通过；实际文件与真实令牌未读取 |
| 完整工程检查 / race / 前端构建 | `scripts/check.ps1` 与专项 race | 全检通过；API/Store Milky 配置 race 非缓存通过；最终前端 JS 418.34 kB / CSS 48.76 kB |
| 真人登录、测试保存、重启与好友消息对账 | 按接入说明操作 | 待 kirinbao 验收 |

新表 `qq_milky_connection_config` 位于 SQLCipher v13；保存凭据与清除全部历史 Milky 接收授权在同一事务完成。读取与保存没有协议端网络调用；候选测试只核验，不更改当前连接。worker 在更换凭据前取消并等待退出，失败保存不替换原连接。配置操作不进入 MCP 或模型工具。

根目录运行源码与独立 Git 分支有不同历史基线，选择性同步结果另记；不得整体覆盖其他未提交开发内容。原开发工作区的未提交 assistant 也占用了迁移 13，未来合并必须先重新编号并检查迁移，不将两份 13 同时交付。当前分支只包含 Milky v13。

完整 `scripts/check.ps1` 通过 Go test/vet、接口契约、PowerShell bootstrap 9 用例与启动器 53 次隔离启动、全部客户端回归、session 11 组、device 31 组、前端格式/类型与生产构建。最后仅调整安装提示的文件名后，Milky 客户端回归、格式、类型与构建再次通过；未重复无变化的后端全检。

当前根运行目录选择性同步新 API/Store/配置表单，保留原鉴权基线；补入 v12 表结构和 Milky v13 迁移，未启用设备鉴权功能。API/Store/integration 测试、Milky 客户端和类型/生产构建通过（JS 402.86 kB / CSS 48.13 kB）。重启后台后仅查询脱敏元数据：配置 GET 成功、source=none、configured=false、默认条数20、无 access_token 字段、加密存储已配置、自动发送关闭、工作台 HTTP 200。未调用真实协议端测试、保存真实 QQ 凭据或读取聊天。重启生成的新产品服务令牌仅留本机剪贴板，未记录到仓库。

## 上游核验

- LLBot Desktop [v3.1.2](https://github.com/LuckyLilliaDev/LuckyLillia.Desktop/releases/tag/v3.1.2)：已从官方固定资产下载并准备在本机忽略目录 `.tools/LLBotDesktop-v3.1.2`，未运行外部程序或登录。ZIP 25,284,046 字节，SHA256 `ddddad913c2a2677a7ed79b051e44574ced9aa5aec7078a3d27c33f60ad2d903` 与 GitHub 发布摘要一致；提取前逐项校验目标路径。入口 `lucky-lillia-desktop.exe`。二进制及 ZIP 不进入 Git 或产品分发。
- LLBot 核心 `de602971f850c3965a9b348e7d1f3e723eb1eba7` [协议定义](https://github.com/LuckyLilliaDev/LuckyLilliaBot/blob/de602971f850c3965a9b348e7d1f3e723eb1eba7/src/milky/generated/schema.ts)为 1.3；[官方配置](https://luckylillia.com/guide/config)说明 Milky 端口 3010 / HTTP 与事件共用，WebUI 是 3080。
- Lagrange.Core `e1bd4baa16fa86ef000154e3eecb7816a29b63a7` [版本定义](https://github.com/LagrangeDev/Lagrange.Core/blob/e1bd4baa16fa86ef000154e3eecb7816a29b63a7/Lagrange.Milky/Constants.cs)仍为 1.2，故纠正旧“安装后可直接使用”推荐；没有放松版本检查。

辅助工程复核和检查不能替代指定人员批准。T02 整项保持进行中、实际完成日期 —；MVP 0/27、扩展 0/5 不变。
