# 白色简约与原生 Agent：工程验证记录

- 日期：2026-11-17（Asia/Shanghai）；基线 `e16fbc5ac9e57f2de3d0443cd0a1aae4ea817d03`，当前分支 `feat/mcp-service`。
- 关联：`CHG-T08-007`、`CHG-T11-004`、`CHG-T12-002`、`CHG-T13-004`、`CHG-T20-003`、`CHG-T21-003`；QQ Milky-only 整合见[独立记录](<T02-milky-only-validation.md>)。
- 状态：实现、整合全检、扩大 race 和隔离 Chromium 检查通过；**指定非作者审查、真实模型/平台兼容性、原生 Tauri 与 kirinbao 正式验收待进行，实际完成日期 —**。AI 辅助检查不等于指定人员审批，MVP `0/27`、扩展 `0/5` 不变。
- 边界：本轮不更新 main、不部署、不替换/重启已有后端；不读取实际凭据或聊天，不连接真实模型、微信、QQ、Telegram 或邮箱。只提交推送当前功能分支，不建 PR。

## 1. 交付与权限边界

1. [官方 guidance 来源](<../../.agents/skills/modern-web-guidance/PROVENANCE.md>)固定至 `GoogleChrome/modern-web-guidance@64a6d708f5a43f2cd3fde5d4d7db3905ba1bdf65`，项目级手工安装。没有可用的官方安装 CLI，不声称已启用 DSH 全局插件；没有执行上游生命周期脚本或修改其 Apache-2.0 正文。
2. 实际界面为白底、深灰字、细线、小圆角，无渐变/玻璃/光晕/装饰统计；[设计规范](<../../DESIGN.md>)与[结构化设计扩展](<../../.impeccable/design.json>)从现有样式和本轮截图提取，不额外改动运行时 token，也不添加外部字体或新装饰。
3. [原生工具协议](<../../internal/agent/tools.go>)使用 `tools/tool_calls` 与匹配 `tool_call_id` 的 `tool` 续轮，不以关键词路由或普通 JSON 建议代替工具执行。[独立协议测试](<../../internal/agent/tools_test.go#L23-L58>)通过合成 HTTP provider 验证两轮实际请求字段、工具选择及回执关联；这项证据**来自后端测试，不来自浏览器 fixture**。
4. [目录](<../../internal/api/assistant_catalog.go>)与[契约](<../../api/openapi.yaml>)对照：原有 62 methods = 44 可执行（42 业务 + 2 health）+ 5 人工专用 + 13 未实现；另 5 个 Assistant 控制接口，共 67 methods。目录覆盖不代表所有连接器已真实账号验收；501 与未就绪如实反馈。QQ 仅 Milky 1.3，OneBot 已退役。
5. 模型只获得固定业务工具，不获得平台/模型凭据、任意 URL、文件、SQL 或主机执行能力。设备授权与模型配置修改保留人工专用。全部 mutation 与 Milky 外呼核验 GET 均逐项人工确认；每轮及每次确认各需新的模型数据同意。详情见[ADR 0014](<../adr/0014-native-assistant-and-confirmed-business-tools.md>)和[助手说明](<../assistant.md>)。

## 2. 后端、契约与客户端检查

实际执行：

```powershell
pwsh -NoProfile -File scripts/check.ps1

go test -race -tags sqlite_fts5 ./cmd/sociald ./cmd/sociald-mcp ./internal/api ./internal/localcredentials/... ./internal/mcp ./internal/store ./tests/integration -count=1 -timeout 240s
```

| 检查 | 实际结果与范围 |
| --- | --- |
| 整合全检第 1 / 2 轮 | 分别在 Milky 整合文件 gofmt、既有 API 客户端 Prettier 处失败；已读取文件并仅格式化相应范围，不把失败记录为通过。 |
| 最终整合全检 | **PASS**；20:16:04.954 → 20:16:52.483 +08:00，47.53 秒。Go sqlite_fts5 测试（部分缓存）、vet、契约、bootstrap 9 子用例、launcher 53、业务客户端、session 11 / device 31、Assistant/Digest 客户端、格式、tsc、Vite 均通过。[检查入口](<../../scripts/check.ps1>)不启动真实后台。 |
| 扩大后端 race | **PASS**；20:06:09.583 → 20:06:45.956 +08:00，非缓存 `-count=1`；sociald 3.497s、MCP 主程序 11.690s、API 23.776s、localcredentials 10.244s、MCP 2.526s、store 25.359s、integration 8.862s。 |
| 原生工具与执行边界 | [provider 协议测试](<../../internal/agent/tools_test.go>)及 [Assistant API 测试](<../../internal/api/assistant_test.go>)覆盖真实工具字段/续轮、严格 JSON、未知工具、认证归属、不可变一次性确认、拒绝无外呼、过期/模型版本、暂停来源、共享额度、取消/unknown、GET 不继续执行。不是实际第三方模型兼容性证明。 |
| 日报与持久化 | 迁移 13、旧无绑定计划禁用、每次模型保存撤销周期同意、CAS 与模型 revision 0 区分、原子共享额度、失败/unknown 禁重发；按单会话限域、证据校验、provider 鉴权不误判为设备 401。见[日报决策](<../adr/0011-daily-digest-and-relevance.md>)。 |
| 冻结构建 | Vite 45 modules；CSS 49,384 bytes / gzip 8.80 kB，JS 438,738 bytes / gzip 130.28 kB。后续只增加设计/文档/证据，不重建或修改这份浏览器输入。 |

日报只是固定未读/方向/问号等文本标记排序后的有界分析，不是已经评测通过的语义相关性或高频联系人排序。已入库私聊和群聊文本可参与；群成员、@归属和群权限尚未建模。默认共享每日 50 次模型尝试，失败也记账；没有自动发送器。

## 3. 隔离浏览器：保留失败与唯一确认轮

### 首轮：fixture 失败，不是产品验证通过

- 20:18:42.003 → 20:18:55.315 +08:00，退出 1；原测试错误要求设备注册前合法的 master-auth `GET /api/v1/system` 必须带设备身份，导致连接超时。
- 只完成“记住本机默认明确选中”一条断言；Agent、Digest、窄屏及五张计划截图**没有**完成。
- 随后只修正测试的三处问题：允许恰好一次合法 bootstrap、等待控件真正可操作但不重复点击/请求、返回契约要求的完整微信 monitor 合成对象。产品代码和最终构建未变。
- 原始报告与失败截图保留在本地 ignored runtime，未被覆盖；失败图 SHA256 为 `5bcd3c04b061adca85a837e560dc3b0653be465abf71d31909347e43d94b9512`。

### 唯一确认轮：PASS

- 包装时间：20:31:45.453 → 20:31:51.560 +08:00；实际测试：20:31:45.614 → 20:31:48.766 +08:00，3.152 秒，退出 0。未增加第二个确认轮，也未继续打磨/重跑产品界面。
- **23 条检查记录**（包含重复场景断言），5 张计划截图齐全；整合者逐张查看。完整断言、冻结输入 manifest、前后指纹、合成请求审计与图片 SHA256 见[可提交证据](<assets/T13-native-assistant/browser-evidence.json>)。
- 全部界面资源由冻结 dist 字节响应，所有业务 API 响应均由合成 fixture 提供；没有真实后端、模型或平台请求。不把 fixture 的硬编码工具状态当作原生协议验证；原生协议另由第 2 节后端测试证明。
- 唯一临时 Chromium profile 使用真实不可导出 WebCrypto P-256 密钥/IndexedDB，fixture 验证新 proof；重载复用设备并获取新会话。合成 master/bearer 不在 Web Storage，密钥不导出。此处是浏览器机制检查，不是实际生产设备授权验收。

| 浏览器检查范围 | 观察结果 |
| --- | --- |
| 白色与布局 | 1440×1000 桌面 Inbox/Assistant；390×844 窄屏 Inbox/unknown/Digest，根背景均 `rgb(255, 255, 255)`，无**页面级**横向溢出。 |
| 设备与草稿 | 1 次注册、2 次签名会话；未保存 AI 草稿跨导航、恢复及窄屏切换仍保留，无保存请求。 |
| 同意与固定确认 | 目标与共享 50 次额度可见，env 模型 revision 0 合法；逐轮/逐项同意默认不勾选，第二次动作不继承；完整参数只读。 |
| 拒绝 | 无需勾选同意即可拒绝；未改变合成业务设置。 |
| 丢响应 | fixture 已执行一次合成设置修改，再故意丢失确认响应；前端保留 run ID，重置同意并门控新 mutation，仅一次 GET 恢复，不重发 start/confirm/业务写入。 |
| unknown | 明确显示待核实与空 HTTP 状态，不当作成功；禁止以盲目重新提交代替核实。 |
| 日报 | 周期计划与一次运行是两种独立且默认未勾选的同意；目标、共享额度、时区、关闭计划能力均有契约/界面覆盖。 |

实际计数：`bootstrapSystemReads=1`、`enrollments=1`、`sessions=2`、`starts=3`、`confirms=1`、`rejects=1`、`runReads=1`、`syntheticSettingsChanges=1`、`usedToday=4`。这些是合成 fixture 计数，不是实际服务的写入或收费次数。

请求审计共 132 条：冻结资源响应 6、可选 favicon 阻断 2、合成 API 124（含预检）。`consoleErrors/exceptions/interceptionErrors` 为空；三条预期网络失败为两次 favicon 阻断及一次故意丢失 confirm 响应，不隐去失败或声称零网络错误。

### 五张原始字节截图

- [桌面收件箱（1440×1000）](<assets/T13-native-assistant/desktop-inbox.png>)
- [桌面固定操作确认（1440×1000）](<assets/T13-native-assistant/desktop-assistant-confirmation.png>)
- [窄屏收件箱（390×844）](<assets/T13-native-assistant/narrow-inbox.png>)
- [窄屏 unknown（390×844）](<assets/T13-native-assistant/narrow-assistant-unknown.png>)
- [窄屏日报计划与同意（390×844）](<assets/T13-native-assistant/digest-plan-and-consent.png>)

截图均为 viewport，不是全页。窄屏 Digest 即时操作位于折叠线以下，两 checkbox/两禁用 action 另以 DOM 断言覆盖。桌面 Inbox 平台栏有组件内部滚动及局部文字需滚动才能看全；这次断言只证明无页面级横溢，不宣称所有内部区域无滚动，也不把该观察藏作“完美响应式”。

### 冻结完整性与清理

20:33:31.993 +08:00，独立只读复核 38 个浏览器输入源及 3 个 dist 文件：集合、SHA256、bytes、mtime 前后相同，新增/删除/变化均为空；没有重新启动浏览器。

```text
source manifest: 6af990994e2c891a1a67566e0d5037df8c1a8c4d0c1e6b8e91f59685cf09a1bf
dist manifest:   bb26406eb25c3f474f513c18e96b494dcab85fe5558c36cf67c8bb7b444f0c79
harness SHA256:  6646d90018693d6b6e6ecd90cfdf8cfde4b801056b3e498fb8cdd101bfc301f1
```

20:33:33.389 +08:00，按本轮唯一合成 profile 精确检查残留 Chromium 为 0；未杀其他实例。原始 runtime 报告保留本机；提交副本不包含 profile 绝对路径、设备 ID、主令牌、bearer 或私钥。图片字节未改，完整指纹在[证据 JSON](<assets/T13-native-assistant/browser-evidence.json>)。

提交前静态复核通过：设计YAML语法/限定字段/引用、14组8步色阶、10个组件和逐字叙述；46个本地文档链接、5张PNG的SHA/bytes/尺寸、38个输入源与3个dist的SHA/bytes/mtime全部相符。已安装211份技能/指南与原始许可证共212文件、2,081,537字节，逐文件与固定上游字节对照一致；唯一额外文件为项目安装来源记录。新增设计、文档和证据未改变已测产品输入。

## 4. 待审查、待验收与下一步

- joey 审后端/API/存储/迁移及非本人脚本；angjustinl 审前端/桌面及非本人脚本；真实审查记录、结论与日期 —。同一 AI 会话及辅助代理不冒充两位协作者。
- kirinbao 授权后再核验真实兼容模型的原生工具往返、实际费用/额度、平台个人号接收、持续连接、原生 Tauri/WebView 设备授权与窄屏可用性；不在本轮擅自读取凭据或执行这些外呼。
- 收到上述证据后再更新审查/验收步骤和实际完成日期；当前各整项仍进行中。只推送功能分支用于审查，不借历史 Milky 运行实例更新记录声称本轮已经上线。
