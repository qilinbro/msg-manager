# T22 测试前有限修复与工程验证

- 日期：2026-11-19，Asia/Shanghai。
- 编号：CHG-T02-007 / FEAT-T02、CHG-T08-013 / FEAT-T08、CHG-T17-002 / FEAT-T17、CHG-T22-001 / FEAT-T22。
- 状态：有限修复已实现，隔离工程复测及指定范围 race 通过；**待指定非作者审查和功能验收，不是全部功能测试通过**。实际审查人、验收人、实际完成日期均为 —；MVP 0/27、扩展 0/5 不变。
- 后端负责人 angjustinl、指定审查 joey；前端负责人 joey、指定审查 angjustinl；测试/交付由 kirinbao 组织，脚本另需非作者审查。AI 辅助复核不等于上述人员批准。
- 交付限定当前 `feat/mcp-service`，不创建 PR、不更新 main。最终实际提交由交付回复登记，不预填尚未生成的哈希；候选可由下述基线、覆盖文件指纹和最终索引校验追溯。

## 1. 范围与不变边界

按用户“先不做了，最后优化一下，然后准备开始测试所有的功能”，暂停个人微信现有好友/群自动回复和定时发送：未实现、未启用，不再扩展发送框架。微信已有本地只读导入/明确授权名单轮询、飞书固定本人私聊早晚报各自保留原授权边界，本轮不代用户启用。

本轮仅修改 10 个源码/测试文件，另同步文档和证据。未使用真实账号、凭据、原始聊天、模型/搜索付费调用或平台发送；未启用计划，未迁移/破坏生产库，未替换现有后台或预览产物。候选只共享前端依赖、Go 模块和编译缓存，构建写入隔离副本；**当前运行实例不是本轮候选，不得拿它验收这些新修复**。

## 2. 冻结版本与首次失败

| 轮次 | 基线与输入 | 真实结果 |
| --- | --- | --- |
| 首轮 | `76ec21e69e771d703a7c2ea97b24d1f42e8f0db2` 加本轮 10 个白名单覆盖文件 | 15:04:23 开始，Go 格式门禁失败，工程阶段 0.382 秒；未进入 Go 功能测试、前端检查或 race |
| 复测 | `a693658820afa002cf064a22552fcc45c61db4d4`，使用命令级 LF 导出，再叠加完全相同的 10 个覆盖文件 | 15:11:02—15:14:47，工程门禁通过，随后三包无缓存 race 通过，进程退出 0 |

首轮错误原文为 `Go files need formatting. Run gofmt and review the changes.`，见[原始失败日志](<artifacts/2026-11-19-pretest/first-run-full-check.log>)、[首轮结果](<artifacts/2026-11-19-pretest/first-run-result.json>)和[格式诊断列表](<artifacts/2026-11-19-pretest/first-run-gofmt-diagnostic.txt>)。不能把这次失败删掉或改记通过。

原因与处理：

1. 首轮导出继承本机 `core.autocrlf=true`，179 个未修改基线 Go 文件被格式门禁列出，5 个本轮 Go 覆盖文件均不在失败列表中。样本[后端入口](<../../cmd/sociald/main.go>)在首轮副本为 88 CR / 88 LF，复测副本为 0 CR / 88 LF；规范化 LF 后 SHA256 相同。
2. 仅将归档命令改为下列形式；没有批量格式化、修改全局 Git 配置或修改基线源码来“刷绿”。5 个门禁/依赖输入在两轮的原字节 SHA 不同，但 Git 规范化 blob 全部相同，见[复现及换行诊断](<artifacts/2026-11-19-pretest/reproducibility.json>)。
3. 准备复测时冻结守卫发现 HEAD 已变化，在创建新副本前停止。核实为另一会话已提交的数字微信群会话修复 `a693658`，随即重新固定该提交；其代码进入复测基线，但其真实导入/模型记录仍属于原会话，不算本轮证据。**复测不是同一整树的原样复跑**；本轮 10 个覆盖文件 SHA256 则两轮完全一致。

```powershell
git -c core.autocrlf=false -c core.eol=lf archive --format=zip <frozen-commit>
```

[首轮冻结清单](<artifacts/2026-11-19-pretest/first-run-freeze-manifest.json>)与[复测冻结清单](<artifacts/2026-11-19-pretest/rerun-freeze-manifest.json>)保留基线、归档 SHA256、覆盖文件及门禁输入 SHA256。[候选/索引一致性记录](<artifacts/2026-11-19-pretest/source-index-check.json>)逐项核对 10 个源码/测试文件和 5 个门禁/依赖输入的规范化 Git blob，15 项均一致，处理了 Windows 暂存时的换行规范化差异。

## 3. 本轮有限修复与自动覆盖

### CHG-T02-007：Milky 身份、整页和接收失效边界

- [历史客户端](<../../internal/connector/qqmilky/client.go>)每次取历史前重新核验本人 QQ 与 Milky 1.3；返回超量、错误 scene/peer 或混合页时整页拒绝，不部分写入，也不把其他会话重标成请求联系人。
- [接收循环](<../../internal/connector/qqmilky/events.go>)在启动和重连时重新核验；身份/版本失效后终止接收，不通过历史补偿跨越身份边界。普通网络/鉴权/协议故障的既有重连和缺口语义保留。
- [API 状态与生命周期](<../../internal/api/qq_milky.go>)撤销运行态核验/接收并取消 worker，但不在 worker 回调中等待自身结束；显式停止在外部收敛。核验代次拒绝晚到的旧成功；保留持久授权名单，不能把保存名单等同持续接收，恢复需要新的显式核验。
- [连接器新增回归](<../../internal/connector/qqmilky/history_boundary_test.go>)与[API/集成回归](<../../tests/integration/qq_milky_test.go>)共新增 7 个顶层测试、36 个叶子场景：历史前核验 5、整页拒绝 6、接收 fatal 6、手动同步/收件箱 9、失效撤权/显式恢复 7、接收错页 2、并发旧核验 1。场景数来自测试源码，不是 36 次真实协议端验收。
- 局限：历史响应没有原子账号绑定；前置 Probe 无法证明两个请求之间服务端绝不换号，仍有协议级 TOCTOU。真实端行为、逐页 next_message_seq 水位、事件持久断点和 QQ 发送均未因此完成。

### CHG-T08-013：取消连接与导入防误关闭

- [会话 hook](<../../apps/desktop/src/useServiceSession.ts>)将无有效连接时的“取消”作为当前页面的显式意图边界，抑制 focus/visibility/设备描述符自动恢复；保留候选、输入和既有设备授权，本页面内由手动提交重新允许验证。取消已连接会话的后台元数据复核不误断开。继续保留 AbortController、请求代次和设备 epoch 守卫。
- [收件箱导入面板](<../../apps/desktop/src/Inbox.tsx>)导入中禁用普通关闭，并保留处理器守卫；顶部入口只打开、不切换卸载。显式取消等待仍可用，直接显示“后台可能已处理”的不确定结果警告，不承诺服务端回滚。
- [会话回归](<../../scripts/test-service-session.mjs>)13 组、[设备授权回归](<../../scripts/test-device-auth.mjs>)32 组通过；[导入回归](<../../scripts/test-import-analysis.mjs>)覆盖实际 Inbox/ImportPanel 的 13 个合成执行场景，包括真实父级关闭路径、排队旧处理器、显式取消、晚到 200/401/网络错误、卸载 abort，以及不写旧状态、不误报鉴权、不刷新/重放写入。
- 延迟请求使用 `Promise.withResolvers()` 控制，不以延时碰运气。测试加载真实模块/编译组件，但采用合成服务和 VM/hooks，不是浏览器、computed AX、原生键盘或 Tauri 验收。
- 局限：不确定结果提示没有新增跨导航持久化；随后显式关闭、导航或刷新仍可能丢失提示。取消请求不等于后台未处理，核实结果前不得盲目重试。

## 4. 隔离工程复测结果

环境：Windows amd64，PowerShell 7.7.0、Go 1.27.4、GCC 14.2.0、Node 24.21.0；前端锁定 React 19.3.1、TypeScript 7.0.2、Vite 8.3.8、Tauri API 2.12.4。Go 采用 `sqlite_fts5`、CGO、`GOTOOLCHAIN=local`、`GOPROXY=off`、`GOSUMDB=off`、`GOFLAGS=-mod=readonly`。进程内清除指定产品配置变量且不打印值，默认凭据路径指向不存在的合成候选路径。完整配置见[复现记录](<artifacts/2026-11-19-pretest/reproducibility.json>)。

在隔离候选根目录运行：

```powershell
& ./scripts/check.ps1
go test -race -count=1 -tags sqlite_fts5 ./internal/connector/qqmilky ./internal/api ./tests/integration
```

| 检查 | 本轮结果 | 证据/限制 |
| --- | --- | --- |
| Go 全源码格式、全量测试、vet | 通过 | 19 个有测试包通过，6 个包无测试文件；[工程日志](<artifacts/2026-11-19-pretest/rerun-full-check.log>) |
| API 契约、受限 Agent/日报/飞书边界 | 通过 | 同上；不是平台联通或发送验收 |
| Windows PowerShell bootstrap / launcher 合成回归 | 通过 | 9 个子例加选择器/参数检查，53 次隔离启动；不访问现有服务或真实账号 |
| 17 个前端离线回归脚本 | 全部通过 | 包含本轮会话、设备和导入回归，以及已有披露/微信/飞书/Agent 检查 |
| 前端全源格式、TypeScript、生产构建 | 通过 | 隔离产物；不是正在运行的 UI，也不是原生安装包 |
| Milky connector / API / integration race | 通过 | `-count=1`；包耗时 6.922 / 21.219 / 10.367 秒，[race 原始日志](<artifacts/2026-11-19-pretest/rerun-milky-race.log>) |
| 本轮完整门禁与 race 阶段耗时 | 148.196 / 76.919 秒 | [复测结果与时间戳](<artifacts/2026-11-19-pretest/rerun-result.json>)；阶段耗时含编译/启动开销，不等于包耗时相加 |

入口为[工程检查脚本](<../../scripts/check.ps1>)，本轮没有传 `-IncludeDesktop` 或 `-IncludeWechatInit`，因此未执行 Cargo/Tauri、微信初始化 Python 依赖/原生安装与真实进程初始化专项，不拿默认门禁通过冒充这些检查。

警告没有被隐藏或改阈值消除：SQLCipher 第三方 C 编译的局部变量地址/复制长度警告、Node 合成测试的实验性 TypeScript strip 警告，以及 Vite 大于 500 kB 的 chunk 提示均保留在日志。JS 为 522756 字节，gzip 日志为 152.70 kB；这不是运行时性能或安全验收，后续按 T23 独立评估，未为本轮扩大依赖或重构范围。

## 5. 产物与证据完整性

[构建产物清单](<artifacts/2026-11-19-pretest/build-artifacts.json>)记录隔离 HTML、JS、CSS 及两份许可证资料的路径、字节数和 SHA256；未上传依赖、缓存、源码归档、真实数据或构建二进制。

| 隔离产物 | 字节数 | SHA256 |
| --- | --- | --- |
| HTML | 588 | `d0f645188a41c0be5498074939c925426632eaca9cdc197a2085432514e1dc4d` |
| JS | 522756 | `c0e6de3ac75a1616f2050fd050a784b90232dc948e2767fd1ed2a346f1aa496e` |
| CSS | 58982 | `3292c59154589784ea7c04140402ec16b0ec76f461e38a2c7ce64c5aa764fefd` |

两轮原始日志、结果与冻结清单共 8 份原样复制，复制前后 SHA256 逐项一致；未删行、替换或重编码。目录本地[归档属性规则](<artifacts/2026-11-19-pretest/.gitattributes>)用 `-text` 保留 Git 提交/检出时的原字节，不更改仓库其他换行策略。额外元数据说明换行诊断、环境、构建指纹与索引一致性。[归档校验清单](<artifacts/2026-11-19-pretest/SHA256SUMS.txt>)覆盖本目录其他证据文件；校验清单自身不作循环校验。上传/构建通过不等于部署、指定审查或产品验收。

## 6. 全功能测试准备与下一步

交付[56 项全功能验收清单](<T22-all-features-test-plan.md>)，FT-001—FT-056 **全部待执行**。工程自动回归不代填这些手工/端到端结果；清单也不是 T22 规定的 100 条固定评测消息。100 条固定评测集尚未制作、指定审阅或执行。

建议下一步：先确认实际运行版本已切换到本轮候选，再由 kirinbao 按“设置/连接→总览→消息与来源→联系人→审批占位→Agent→飞书→设置”逐项用合成数据记录结果。真实账号只读、模型/搜索费用、发送/周期启用分别另行授权；遇 unknown 先只读核实，不自动重试。需要切换/部署时另行安排，不能把现有旧实例当新候选。

仍待执行/审查的范围：

- 后端 joey、前端 angjustinl、脚本非作者的正式审查与 kirinbao 验收；本轮不代填人员结论。
- CHG-T08-012 的两批浏览器失败保持失败，修正后键盘/computed AX/交互仍待验，本轮没有启动第三批浏览器验证。详见[既有披露验证记录](<T08-help-details-validation.md>)。
- 原生 Tauri/安装、24 小时稳定性、大数据性能、安全/故障和备份恢复待独立执行；T23/T24/T25/T26 不自动开工或完成。
- 通用规则、任务、审批、发件箱、发送、SSE 仍有占位/未实现；`/health/live` 的可用不代替 `/health/ready` 的通用无人值守就绪，后者当前预期 503。本轮未对现有服务作新的健康探测。
- 飞书 21001 平台策略阻塞保留，未再次请求；Telegram 个人号分支未合并/部署，ClawBot 不冒充已有能力。微信个人号自动回复/定时发送继续暂停。
- [技术文档](<../../技术文档.md>) §47—48 的真实模型/迁移/微信群导入及其他会话记录只作各自历史证据，不并入本轮通过数量；真实 Milky 接收、模型费用、平台发送和持续运行不能由模拟结果替代。
