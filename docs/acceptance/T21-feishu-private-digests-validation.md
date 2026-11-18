# 飞书本人早晚报：工程验证与待验收记录

- 日期：2026-11-17（实现、返工及工程检查）；2026-11-18（证据归档与功能分支交付收尾），均为 Asia/Shanghai。
- 范围：`CHG-T12-003` / `CHG-T13-005` / `CHG-T16-001` / `CHG-T17-001` / `CHG-T20-004` / `CHG-T21-004`。只向本人飞书私聊发送有界相关摘要、待人工核对的候选待办与最少证据摘录；每天上海 08:00 / 20:00。
- 实现及下列工程检查已通过，子项待指定审查 / 待真实验收，实际完成日期仍为—。没有读取真实聊天或凭据，没有调用真实模型/飞书，没有开启真实计划；不能证明应用权限、本人归属、平台可达性、手机通知或已读。
- 指定非作者审查：后端由 joey、前端由 angjustinl 审查，仍待进行；测试/交付脚本由非作者技术成员审查。kirinbao 组织真实验收，仍待进行；自动化测试与 AI 辅助检查不代替上述人员。
- 未修改 main、未部署或重启运行中的后端；没有启动替代真实应用服务。MVP 0/27、扩展 0/5 不变。

## 交付与契约

- [接入说明](<../feishu-setup.md>)：仅在本机专用配置表单输入应用密钥及本人同应用 `open_id`；不要在聊天中提交。
- [ADR 0015](<../adr/0015-feishu-private-digests.md>)：冻结窗口、绑定/暂停失效、持久一次性账本和 unknown 边界。
- [API 契约](<../../api/openapi.yaml>)：新增 10 个飞书方法、18 个 schema，全部方法合计 77 个；无任意文本/接收者/URL/scopes 或 force 重试入口。
- [连接器](<../../internal/connector/feishu/client.go>)与[合成连接器测试](<../../internal/connector/feishu/client_test.go>)：OAuth 最小权限、固定目的地址、无代理/重定向/自动重试、严格显式成功、秘密不回显。
- [存储回归](<../../internal/store/feishu_test.go>)、[API 回归](<../../internal/api/feishu_regression_test.go>)及[在途隐私屏障测试](<../../internal/api/feishu_privacy_test.go>)：真实 SQLCipher 合成夹具及 fake provider/sender，不访问本人账号或真实模型。
- [飞书面板](<../../apps/desktop/src/FeishuPanel.tsx>)与[客户端](<../../apps/desktop/src/feishu-api.ts>)：白色简约、独立未勾选同意、冻结正文核对、只读恢复、平台接受不冒充手机收到。

## 工程检查

| 检查 | 命令/方法 | 本轮结果 |
| --- | --- | --- |
| 返工后全量工程检查 | `pwsh -NoProfile -File scripts/check.ps1` | 通过，退出 0，83.853s；Go 格式/全仓 test/vet、契约、bootstrap 9 子用例、launcher 53 次隔离启动、全部新旧客户端/组件回归、前端格式/TypeScript/生产构建；API 28.308s，未变更包可复用 Go 缓存 |
| 返工后 API/存储/生命周期及连接器 race | `go test -race -tags sqlite_fts5 ./cmd/sociald ./cmd/sociald-mcp ./internal/api ./internal/localcredentials/... ./internal/mcp ./internal/store ./internal/connector/feishu ./tests/integration -count=1 -timeout 240s` | 通过，退出 0，43.972s；API 31.465s、Store 33.556s、连接器 4.603s；非缓存运行，无真实平台请求 |
| 停用竞态确定性红/绿回归 | `go test -race -tags sqlite_fts5 ./internal/api -run '^TestFeishu(ScheduledGenerationRechecksScheduleEpoch\|ScheduleRevocationCancelsLateRegisteredJobs)$' -count=20 -timeout 120s` | 修复前非 race 单轮：四组 epoch 反例和晚注册反例失败、两组未变更正对照通过；修复后专项 race 连续20轮通过，退出0，33.298s（Go测试26.178s）；撤销后0模型调用/0额度、无正文/无投递，正对照可正常生成并投递 |
| OpenAPI 与权限边界 | `node scripts/check-contract.mjs`（亦纳入全检） | 通过；严格输入、200/202、凭据 human-only、unknown/no-retry、关闭计划仅三键及旧本机摘要兼容断言 |
| 飞书客户端/面板 | `node scripts/test-feishu-client.mjs`（亦纳入全检） | 通过；10 方法、DTO/绑定/日历/数组上限、凭据清理、独立同意、只读恢复、已知投递不因报告 null/拒绝/超时丢失 |
| 原生 Agent 飞书确认 | `node scripts/test-feishu-assistant.mjs`（亦纳入全检）及 Go Assistant/API 回归 | 通过；发送/启用另勾外发风险、模型同意独立、冻结参数、202/pending/unknown 不记成功、不自动重放 |
| 浏览器批次前独立 fresh build | `npm --prefix apps/desktop run build` | 通过，退出 0，1.618s（Vite 181ms）；48 modules，JS 487.35 kB / CSS 53.75 kB；返工后全检再次构建（Vite 202ms），3 份 dist 内容仍与该批次相同，不声称 mtime 相同 |
| 桌面/窄屏浏览器合成检查 | 同一批隔离 Chromium，桌面 1440×1000 / 窄屏 390×844 | 首批通过，退出 0，3.635s；45 条检查记录、6 张截图均已逐一查看；无浏览器修正或确认重跑 |
| 最终集成差异审阅 | `git diff --check`、`git diff --cached --check`、暂存范围及逐模块审阅；OpenAPI 使用 histogram 复核 | 通过；本轮66文件，未暂存差异为空；7份文档本地链接有效，17份证据的索引blob与原始bytes一致，16份原/新归档及3个返工源码SHA-256再核对通过；AI辅助复核不替代指定人审 |

返工后原始输出：[全检](<artifacts/2026-11-17-feishu-private-digests/engineering-check-post-revocation.log>)、[扩大 race](<artifacts/2026-11-17-feishu-private-digests/backend-race-post-revocation.log>)、[修复前确定性反例](<artifacts/2026-11-17-feishu-private-digests/schedule-revocation-red.log>)、[修复后专项 race×20](<artifacts/2026-11-17-feishu-private-digests/schedule-revocation-race-green.log>)。保留返工前历史输出：[首批全检](<artifacts/2026-11-17-feishu-private-digests/engineering-check.log>)、[首批扩大 race](<artifacts/2026-11-17-feishu-private-digests/backend-race.log>)、[浏览器前 fresh build](<artifacts/2026-11-17-feishu-private-digests/fresh-build.log>)；不将它们冒充覆盖后端返工。

原样日志含 Windows CRLF，红测试另有4处 `t.Fatal` 输出行尾空格。首次差异检查因此报空白问题，没有清洗日志或改写历史manifest；[仅本批证据的属性](<../../.gitattributes>)关闭换行转换并认可CRLF，仅红日志额外允许原始行尾空格，源码/文档保持默认规则。重新检查通过，17份归档文件在Git索引中也与原件逐字节一致。

## 合成覆盖与事实边界

- 连接器：最小 OAuth 表单、opaque 长 token、有效期/单配置缓存切换/并发合并、双层 JSON 中文与转义、字节预算、无请求重试、HTTP/业务/坏 JSON/重复字段/取消的固定安全错误、凭据/bearer/OpenID 回执反射禁止、矛盾响应 unknown。只使用 fake RoundTripper。
- 存储：固定上海半开窗口、绑定参与内容哈希、配置 CAS/保留秘密/每次保存撤销计划；16 并发报告与 16 并发投递各仅一人领取，换目标/配置不能重投；30 次混合额度领取在额度 7 下只成功 7 次；SQLCipher 关闭重开持久额度与 running→unknown。
- 来源与报告：只读已入库且未暂停、相关性分数≥3的消息；最多 500 候选、4 个会话×20 条。检查所有模型输入来源的 revision，不只展示出的证据；覆盖边界 ±1ns、暂停后恢复、删除保留引用、501 条候选限界、模型草稿不得成为待办、外来证据/秘密反射/超限及非法嵌套报告拒绝。
- 调度/投递：启用严格从未来时点开始；2 分钟运行期 grace 边界、启动/迟到不补跑、手动预览占用窗口、下一个时点后不得手动发送旧报告、任意投递状态永久占用窗口、只写一次终态、重启恢复及关闭生命周期。
- 在途隐私屏障：暂停发生在首个 Analyze 中时取消/等待，后续会话不再开始调用，未知报告不可读；配置保存发生在 Send 中时取消/等待后才响应，投递保持 unknown，改目标不能重投。已在途或平台已接受的消息不能承诺撤回。
- 原生 Agent：新增 8 个非凭据可执行工具、2 个 human-only 配置方法；服务器冻结报告/正文/目标/哈希与绑定，只接受报告 ID，不接受任意文本或收件人。HTTP202 或嵌套 running→pending，unknown 保持未知。HTTP 关闭计划无需模型/飞书凭据，只允许三键；Native 关闭实际请求不混入四项绑定，清除飞书配置后仍能确认关闭。
- 前端：五处模型 ID 校验均要求 64 位小写十六进制，允许环境模型 revision 0；来源引用/会话≤4、待办≤8、摘录≤3。App Secret/原始 Open ID 不进 React state/Web Storage/URL，保存 finally 的所有成功/失败分支与离开均清理；非秘密 App ID 可在组件状态中显示。六组成功/失败回执×报告 null/拒绝/超时均能保留已知投递，只读获取正文不触发 POST。App 内恢复缓存只放元数据，不放正文/哈希/凭据/同意。
- 实现期间发现的相关性门槛、Analyze 子超时结果、敏感回执反射、关闭计划多余绑定、凭据清理与已知回执恢复缺口，已在浏览器批次前修正并加入回归。该批次之后最终集成复核另发现计划停用竞态，按下一条返工；上表最终全检/race 是返工后重新执行，不沿用旧结论。
- 计划停用竞态返工：首次取消快照后才注册的生成任务可能遗漏取消。计划 PATCH 取得派发写锁后再次取消；每次模型额度预留/Analyze 及最终正文落库前持读租约检查 enabled、计划 revision、完整绑定和 LastWindowEnd，故停用、同绑定重新启用、同值重存及空报告均不能复活旧授权。合法占窗后的未取消 ctx 反例与 runtime mutex/读租约控制的晚注册反例不依赖 sleep；撤销后 0 模型调用/0 额度/无正文投递，未变更两组正对照允许正常生成/发送，手动默认关闭场景保持兼容。红测试仅采用修正夹具初始 schedule revision、确认所有授权变更为 HTTP200 后的日志，初版夹具 CAS409 不算漏洞复现。AI 只读复核未发现新增阻断，不代替指定人员审查。
- 这是独立的飞书发送子流程；本机 `/digest` 兼容语义不变，通用会话与 QQ 发送仍未开放。规则相关性不是语义完备检索，不承诺覆盖每条重要消息；群成员、群提及与群权限未建模，候选待办不是已执行任务。

## 冻结构建浏览器证据

- 原首批 UI freeze/build 边界：`2026-11-17T14:30:19.0897917Z`（不是其后后端返工的冻结边界）。浏览器从 `14:30:37.677Z` 至 `14:30:41.312Z`；隔离 Chrome 149，临时 profile 已清理，没有启动真实应用服务器。
- 浏览器准备阶段只做静态检查，修正了 fixture 的 64hex 模型 ID、32 字节 challenge、桌面 npm manifest 路径和精确 OPTIONS 等问题。准备脚本最后写入晚于全检最初构建边界，因此单独重新 fresh build；业务源码未变。准备阶段没有执行浏览器；实际唯一一批即通过，无隐藏重跑。
- [52 个输入源 manifest](<artifacts/2026-11-17-feishu-private-digests/source-manifest.json>)和[3 个 dist 文件 manifest](<artifacts/2026-11-17-feishu-private-digests/dist-manifest.json>)记录相对路径、SHA-256、bytes、mtime；脚本在批次前后断言集合及这些字段完全不变。输入范围是桌面源码/元数据、相关飞书 API/Store/契约与本地 helper，不是全仓指纹；helper 属本机忽略路径，不随交付源码安装。
- 后端返工后的[独立指纹核对](<artifacts/2026-11-17-feishu-private-digests/post-revocation-verification.json>)保留原 manifest 不变：52 项中 50 项内容一致（含全部 40 项前端），只有 [API 停用屏障](<../../internal/api/feishu.go>)、[生成授权复核](<../../internal/api/feishu_worker.go>)两个后端生产文件变化；新增[隐私回归](<../../internal/api/feishu_privacy_test.go>)不在原 52 项内，三者现值另有指纹。返工后全检重建的 3 份 dist 大小与 SHA-256 相同，mtime 不声称相同；9 份浏览器原始 JSON/截图与原输出 SHA-256 再核对一致，16 份原/新证据另列清单。不为纯后端修复重跑 UI 浏览器，也不将旧浏览器报告冒充真实后端端到端覆盖。
- [浏览器报告](<artifacts/2026-11-17-feishu-private-digests/report.json>)保留 45 条检查记录（含多个按钮态断言）、6 组布局、允许请求与计数。默认关闭、独立同意、离开/保存清理、冻结正文以文本显示、202 只读恢复、unknown 不重发、配置失效后收回正文但保留最小回执均通过。
- 真实浏览器 WebCrypto/IndexedDB 生成不可导出 P-256 密钥；刷新前后同设备，fixture 独立验签并签发两次合成会话，未持久化 bearer。配置保存 1 次、启用/关闭各 1 次、预览/发送各 1 次均为拦截的合成请求；Assistant 只启动 1 次并拒绝 1 次，确认次数 0，不执行实际发送。
- 6 组布局背景为白色，无页面横向溢出；三组窄屏交互控件均至少 48px 高。桌面部分控件为 32–44px，不声称所有桌面目标达到 44px。截图为对应节区滚动视图，不是完整页面或全量内容的视觉验收。
- 浏览器异常、console error、意外目标、拦截错误及无法解释的请求失败均为 0；另记录 5 个页面切换造成的预期 `ERR_ABORTED`，不将它们隐去或称作零网络取消。
- 前端完全使用冻结 dist 和拦截的合成 API。浏览器 fixture 仅提供一个 `send_feishu_report` 目录项，用来检查独立风险勾选和不可变参数，且没有确认；不能代替 Go 原生工具协议、完整 Agent 目录或真实后端状态转换的验收。
- 未覆盖原生 Tauri/WebView、物理触控、读屏、完整键盘路径、对比度/缩放/320px；隔离使用页面拦截及 CSP，不等于 OS 级网络防火墙审计。只读恢复 Map 的检查依赖 React 私有 fiber 字段，属测试脆弱点，不是应用公开 API。

| 截图 | 所示范围 |
| --- | --- |
| [桌面：默认关闭](<artifacts/2026-11-17-feishu-private-digests/01-desktop-default-off.png>) | 固定时点、未勾选的周期/本次同意、禁用按钮 |
| [桌面：冻结报告](<artifacts/2026-11-17-feishu-private-digests/02-desktop-frozen-report.png>) | 完整拟发送正文、窗口/目标/绑定哈希、独立发送同意 |
| [桌面：Agent 风险确认](<artifacts/2026-11-17-feishu-private-digests/03-desktop-native-assistant-feishu-risk.png>) | 服务端固定参数及额外飞书外发风险，不实际确认 |
| [窄屏：冻结报告](<artifacts/2026-11-17-feishu-private-digests/04-mobile-frozen-report.png>) | 正文/长 ID 换行和证据折叠区 |
| [窄屏：unknown 回执](<artifacts/2026-11-17-feishu-private-digests/05-mobile-unknown-receipt.png>) | 未知状态、缺少平台接受凭据、人工核对与禁止重发 |
| [窄屏：失效重读](<artifacts/2026-11-17-feishu-private-digests/06-mobile-failed-reload.png>) | 配置冲突、旧正文撤回、未解决投递元数据与配置状态 |

## 真人与真实平台验收（未进行）

- [ ] joey 审查后端、迁移、锁顺序、账本、防重、最小权限和关闭生命周期。
- [ ] angjustinl 审查前端、无默认同意、敏感字段清理、只读恢复及窄屏/键盘操作；测试/交付脚本另由非作者成员审查。
- [ ] kirinbao 在本机配置本人企业自建应用，确认机器人启用/发布/可用范围及唯一权限 `im:message:send_as_bot`；自行核对同应用本人的 `open_id`。
- [ ] 一次真实模型与生成预览：内容限域、相关性、待办证据、费用、暂停与截断提示均可接受。
- [ ] 独立确认一次外发，核对后台回执、飞书本人私聊实际消息和手机通知设置；分开记录“平台接受 / 手机收到通知 / 本人已读”，不混为一谈。
- [ ] 独立启用未来 08:00 / 20:00 计划并在本机服务持续运行且联网条件下核对；关机/休眠不承诺运行，不补发离线旧时段。
- [ ] 真实关闭/清除/暂停及模型/配置保存后不得再开始新外呼；在途消息不能承诺撤回。
- [ ] `unknown` 由本人到飞书核实，不用重发来试探；失败/未知/已有预览占用窗口后不得因换配置、目标或重启重算/重投。

## 提交与交付

- 工程检查结束后仅提交并推送当前功能分支 `feat/mcp-service`，不创建 PR，不更新 main，不部署。
- 当前提交哈希在实际生成前不填写；推送成功不代表指定审查或真实验收通过。
