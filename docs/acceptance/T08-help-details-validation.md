# T08｜全项目辅助说明与成功明细默认折叠

- 日期：2026-11-18—2026-11-19（Asia/Shanghai）。
- 关联：FEAT-T08 / CHG-T08-012；交付分支：`feat/mcp-service`。
- 状态：默认折叠及10处读屏描述关联源码修正已交付；修正后17组离线前端回归、格式、类型、生产构建通过。**两批浏览器均未通过，修正后的原生键盘/computed AX仍待验**；没有第三批。指定非作者审查及kirinbao验收待进行，实际完成日期 **—**。
- 指定职责：joey 负责前端/UI，angjustinl 正式审前端，脚本由非作者技术成员正式审查，kirinbao 组织交互与最终验收；普通 AI 辅助复核不代表人员批准。

## 1. 交付与保留边界

- 新增共用 [HelpDetails](<../../apps/desktop/src/HelpDetails.tsx>)，以原生 `details/summary` 默认关闭展示可选说明、成功时间/数量/回执。保留原文，无持久化、无额外依赖，不用全局 CSS 隐藏段落；外部描述引用的修正见§3.3。
- 覆盖概览、连接/模型设置、统一消息/导入、微信、邮箱、QQ Milky、摘要、会话 AI、联系人档案/关联、飞书；助手复用已有原生“操作详情”“查看带入内容”“更多”。11 个模块采用共用组件，助手不再套一层多余组件。
- [共用样式](<../../apps/desktop/src/styles.css>) 保留原生 marker、可见焦点与换行，桌面入口最小 38px、窄屏/触摸 44px；折叠内容不嵌入表单 label。这是实现边界，不代表键盘与展开态几何验收已通过。
- 错误、unknown、停止等待但后台可能仍在处理、未保存配置、实际历史窗口受限/漏收、排队/运行进度和简明连接状态不收起。混合通知在产生时显式区分成功/警告，不按文本或后来的 busy/error 状态猜测性质。
- 模型数据/费用及独立发送同意留在当前操作旁；选择微信会话或准备启用/修改非空后台名单时，影响提示直接可见。记住设备保留原默认值与 90 天标签，勾选时直接提示共享电脑风险。
- 聊天正文、生成的摘要/待办/草稿/记忆/档案文档及飞书冻结预览仍是业务内容，不作为成功元数据收起。
- 辅助复核发现并修复：导入成功不等于 AI 已完成，`ai_queued > 0` 在“导入结果”外显示简短排队状态；邮箱 `skipped > 0` 即使 `limited=false` 也直接提示结果不完整，并使来源摘要“需检查”。零排队与零跳过不会伪造警告。QQ 静态范围说明仍可折叠，已有实际缺口计数保持可见。

## 2. 修正后的离线工程检查（2026-11-19）

修正后首次回归在第16项微信测试停止：旧“闭合可见文字”模型把局部裁切的读屏镜像也当作常驻视觉文字。只校正该模型，并增加实际镜像内容/类名保留断言；随后**重新全跑17项**及格式、类型、构建，10:45退出0。[首次输出](<artifacts/2026-11-18-help-details/post-fix-2026-11-19/engineering-first-attempt.txt>)、[最终完整输出](<artifacts/2026-11-18-help-details/post-fix-2026-11-19/engineering-results.txt>)均原样保留，[工程记录](<artifacts/2026-11-18-help-details/post-fix-2026-11-19/engineering-record.json>)记录原始日志SHA及29个变更代码文件指纹（不是全仓库清单）。

| 检查 | 实际结果 / 范围 |
| --- | --- |
| 17 项前端回归 | 全部退出 0；配置、设备/内存会话、导入、会话、档案/关联、助手/摘要、飞书、微信/邮箱、Milky、图标等实际客户端/组件的离线合成检查；未运行真实模型/平台 |
| 新 [折叠组件回归](<../../scripts/test-help-details.mjs>) | 实际编译组件、React SSR 转义、原文保留、原生关闭默认值、外部镜像/唯一ID及10处源码守卫、无持久化/全局隐藏、11模块与助手原生入口；已加入 [检查入口](<../../scripts/check.ps1>) |
| 真实组件测试加载器 | [加载器](<../../scripts/frontend-help-test-loader.mjs>) 编译实际 TSX；相关 VM harness 使用实际 HelpDetails，不用丢失折叠语义的假组件 |
| [导入回归](<../../scripts/test-import-analysis.mjs>) | 成功计数默认折叠，正排队状态可见，零排队不显示进行中提示；模型绑定、默认不同意及不重试边界保持 |
| [微信/邮箱回归](<../../scripts/test-wechat-monitor.mjs>) | 成功计数折叠，limited 与 `skipped=1, limited=false` 直接可见并传递需检查；停止等待→ready刷新清除uncertain→迟到响应均不隐藏/覆盖警告；视觉模型明确不等于CSS/AX验收 |
| [设备连接回归](<../../scripts/test-device-auth.mjs>) | 勾选/取消记住设备时即时风险与 `aria-describedby` 对应更新，持久连接安全边界不变；不是浏览器computed AX验证 |
| 格式 / 类型 / 构建 | 全部前端源文件Prettier、`tsc --noEmit`、Vite生产构建通过；仅大chunk、实验性类型剥离及Git行尾非阻断警告 |
| PowerShell入口 | AST解析0错误，未执行完整入口；未重跑完整Go/race或原生Tauri，不挪用历史通过结果 |
| 最终 diff | 源码/证据范围与三文档已复核，暂存 `git diff --check` 通过（字节保真归档例外见§3.4）；29个代码文件与通过时指纹一致，真实提交结果在交付回复登记 |

等价复查命令（仓库根目录，使用项目要求的Node及已有前端依赖；构建不能与冻结浏览器批次并行）：

```powershell
$checks = @(
  'profile-links', 'import-analysis', 'ai-model-client',
  'model-presets-client', 'ui-icons', 'help-details', 'milky-client',
  'profiles', 'service-session', 'device-auth', 'assistant-client',
  'assistant-help', 'chat-discovery', 'feishu-client', 'feishu-assistant',
  'wechat-monitor', 'inbox-import'
)
foreach ($check in $checks) {
  node "scripts/test-$check.mjs"
  if ($LASTEXITCODE -ne 0) { throw "Regression failed: $check" }
}
node apps/desktop/node_modules/prettier/bin/prettier.cjs --check apps/desktop/src
if ($LASTEXITCODE -ne 0) { throw 'Formatting failed' }
node apps/desktop/node_modules/typescript/bin/tsc --noEmit -p apps/desktop/tsconfig.json
if ($LASTEXITCODE -ne 0) { throw 'Type checking failed' }
node apps/desktop/node_modules/vite/bin/vite.js build apps/desktop
if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
```

## 3. 隔离桌面与窄屏浏览器证据

### 3.1 首批失败：测试准备错误，不算产品通过

- [原始报告](<artifacts/2026-11-18-help-details/first-run/report.json>)、[原始摘要](<artifacts/2026-11-18-help-details/first-run/summary.md>)、[指纹清单](<artifacts/2026-11-18-help-details/first-run/manifest.json>)、[脱敏CDP轨迹](<artifacts/2026-11-18-help-details/first-run/cdp-trace.jsonl>)及42张原始失败截图均保留，未覆盖首报。
- 首批退出1：6项未连接概览观察、44条finding（含连锁中断/超时），**0个API GET/OPTIONS，未建立合成服务连接**。`until`把带React引用的DOM返回给CDP按值序列化，产生`Object reference chain is too long`；另有Shift+Tab误把CDP修饰位写成Alt=1（正确Shift=8），安全断言阻止了错误控件被激活。后续未连接场景不能当成产品失败或通过。
- [事后只读分析](<artifacts/2026-11-18-help-details/first-run/first-batch-analysis.json>)纠正原始摘要的无条件模板文案：首批尚未取消记住设备/执行连接，也未完成键盘、AX和同意检查；原报的指纹/拦截`false`是未到末尾核对的初始状态，不是发现文件变化或漏拦截。另行核对40源文件、5构建文件及2个保留harness文件的指纹相符；8个观察到的HTTP请求ID全被Fetch截获，continue=0，业务写入/真实外呼=0。
- 根审阅[首张桌面失败图](<artifacts/2026-11-18-help-details/first-run/failure-connect-synthetic-only-1440.png>)发现平铺伪影。[42图只读像素核查](<artifacts/2026-11-18-help-details/first-run/first-batch-image-audit.json>)仅该图存在758×426完全重复；其他41图未出现同周期重复，两个连接中断时DOM文字各只有一套工作台，不能把截图伪影归因产品DOM。

### 3.2 唯一修正确认批：仍失败，不能写成全项目通过

先集中修harness：谓词按Boolean序列化、Shift+Tab=8、先决失败则跳过依赖用例、finally核对指纹/拦截、按实际执行报告；固定初始1440×1000窗口并记录布局/视觉视口、DPR、单根/侧栏数量。14项纯内存harness检查及57项fixture GET契约通过，不启动浏览器；随后仅此一次确认，未新增服务器、放行真实请求或追加第三批。

- [第二批报告](<artifacts/2026-11-18-help-details/confirmation/run-ZKuJM8/report.json>)、[原始摘要](<artifacts/2026-11-18-help-details/confirmation/run-ZKuJM8/summary.md>)、[指纹](<artifacts/2026-11-18-help-details/confirmation/run-ZKuJM8/manifest.json>)、[CDP轨迹](<artifacts/2026-11-18-help-details/confirmation/run-ZKuJM8/cdp-trace.jsonl>)均保持原字节。结果：**119条通过断言、50条finding；44计划场景仅2个连接/预检综合场景执行且整体failed，0场景passed、42 skipped**，不是42个产品故障。
- 1440×1000、390×844均合成连接成功；默认记住设备初始true，两次均以原生输入取消并硬断言false后才连接。14个合成GET+14个OPTIONS+8个冻结静态请求均拦截，0业务写入/转发/真实平台或模型请求；finally执行，旧版源/dist/harness指纹未变。
- 两个视口共16个辅助入口的默认关闭、summary/内容存在、原生Tab可达及关闭内容不可见断言通过；无详情嵌入label。checked记住设备风险可见，原生取消后风险与ARIA引用切换通过。
- **48条键盘finding不是48个产品缺陷，也不能改成通过。** Enter keyDown缺少字符/`unmodifiedText`，而Space有字符，第一次Enter未激活使后续Space期望反向；这是离线trace及harness诊断，未再次注入作因果复验，原始结果保留为失败。原始16条“expanded overflow”及16条“Enter closes”标签有前置状态失效，不能算有效展开态几何或原生键盘序列通过。
- **另2条AX范围finding独立于键盘故障**：每个视口33个可见控件未报名称失败，但 `service-token → token-description`、`ai-model-preset → ai-model-preset-help` 的DOM目标有文本，computed description缺失。AX在取消记住/键盘序列之前采集，不能归咎Enter。checked checkbox只断言描述非空，不证明两段完整；unchecked没有AX采样。轨迹没有原始AX nodes，不能离线还原。
- 已逐张直接审阅[1440桌面原图](<artifacts/2026-11-18-help-details/confirmation/run-ZKuJM8/connection-1440.png>)和[390窄屏原图](<artifacts/2026-11-18-help-details/confirmation/run-ZKuJM8/connection-390.png>)：单一工作台表面，未见首批平铺；两图尺寸、单根及DOM/CDP视口指标相符。但只覆盖连接设置视口，不是展开态、后续页面或修复后验证。

完整已执行/42项跳过清单、标签限定和无网络静态诊断见[实际覆盖说明](<artifacts/2026-11-18-help-details/confirmation/confirmation-coverage.md>)、[独立分析](<artifacts/2026-11-18-help-details/confirmation/confirmation-analysis.json>)、[图片分析](<artifacts/2026-11-18-help-details/confirmation/confirmation-image-audit.json>)。概览后续、消息/档案/AI/导入、微信/邮箱/QQ状态与停止/刷新/迟到响应、摘要、飞书及助手独立同意等42个依赖场景**没有执行**，不能拿离线回归或连接截图替代浏览器验收。第二批原报告与PNG仅绑定**2026-11-18 22:34旧冻结版**，不证明后续修正。

### 3.3 2026-11-19源码返工：10处描述目标移出关闭详情

系统排查同类外部引用，修正[连接设置](<../../apps/desktop/src/App.tsx>)2处、[模型设置](<../../apps/desktop/src/AIModelPanel.tsx>)1处、[微信](<../../apps/desktop/src/WechatPanel.tsx>)2处、[飞书](<../../apps/desktop/src/FeishuPanel.tsx>)4处、[助手](<../../apps/desktop/src/AssistantPanel.tsx>)1处：

- 共用组件新增可选只读`descriptions`，在关闭的native details之外提供纯文本镜像；助手原生详情采用同等外置span。对外ID仅留在镜像，展开正文不保留重复ID，二者复用同一规范字符串。
- 连接授权有效日期及记住设备条件与当前界面同步，无描述实例不加外壳；有描述时外壳保留整行网格和`min-width:0`。不引入状态、持久化、键盘覆盖或依赖。
- 镜像只用局部1px绝对定位/裁切，不用`hidden`、`display:none`、`visibility:hidden`或`aria-hidden`。说明不常驻显示，关键授权/数据/费用风险仍按§1直接可见。
- 已通过实际组件SSR转义、镜像位于详情之前/唯一ID、局部样式和10处源码守卫及17项工程复验。**没有修正后的浏览器computed AX/完整原生键盘/展开态溢出证据，仍待验**；不修改原始失败报告，不再修harness或新增浏览器循环。

### 3.4 归档与复现边界

[首批harness](<artifacts/2026-11-18-help-details/synthetic-browser.mjs>)与[fixture](<artifacts/2026-11-18-help-details/fixtures.mjs>)留在原位；第二批在[独立归档说明](<artifacts/2026-11-18-help-details/confirmation/README.md>)下，包含修订harness/差异、14项静态检查、原始6份结果及派生分析。[逐项复制校验](<artifacts/2026-11-18-help-details/confirmation/copy-verification.json>)确认15份文件源/归档SHA一致，没有覆盖首报，没有复制Chromium profile、缓存或个人配置。

归档[Git属性](<artifacts/2026-11-18-help-details/.gitattributes>)禁止行尾转换，并在常规空白规则中识别原始CRLF。仅两份原始终端日志及原始harness差异豁免空白检查，以保留ANSI对齐、差异上下文及SHA；其余归档、源码和文档仍检查。没有为消除Git提示而改写证据。

归档脚本按自身目录上两级确定仓库根，**不能在归档目录直接运行**。确需另行授权复现时，先恢复所选批次到仓库`.runtime/T08-helper-details-20261118/`原深度，并使用该批次匹配、已审核的构建；不能把旧脚本在新构建上运行后冒称复现原证据。原始派生说明中的旧源码相对路径/行号也是原运行层级定位；以本节当前源码链接为准。归档不是新增浏览器或真实账号测试的授权。

## 4. 原运行地址与构建核对

### 4.1 2026-11-18 22:34旧版本证据（不是修正后证据）

[旧静态GET记录](<artifacts/2026-11-18-help-details/preview-get-final-2234.json>)中3次精确静态GET均200，HTML/JS/CSS字节SHA与当时冻结产物相符；补上AI排队/邮箱漏收警告，但**尚未包含19日10处描述关联修正**。保留原始记录，不把其“final”文件名解读为本次最终版本。

### 4.2 2026-11-19 10:49:38修正后最终产物

只对原 `http://127.0.0.1:1420` 执行3次下表精确静态GET，禁止代理与跳转；均200且与本地新构建等长、SHA一致。[原始核对JSON](<artifacts/2026-11-18-help-details/post-fix-2026-11-19/preview-static-get.json>)记录范围：无浏览器、未执行线上JS、业务API请求0、真实模型/平台请求0、后台/预览进程未重启。

| 静态路径 | 字节数 | SHA-256 |
| --- | ---: | --- |
| `/` | 588 | `870930e503f3bc4dd3ecdb19fed3d3de956a5b057b02dd03214b95d72935e3b1` |
| `/assets/index-GfP84JgX.js` | 522673 | `4d7562f35359097066f9556282ff1c6f343bb77ed7b5d9d0c364996588692f3c` |
| `/assets/index-ByOR5dor.css` | 58982 | `3292c59154589784ea7c04140402ec16b0ec76f461e38a2c7ce64c5aa764fefd` |

已有预览读取重建产物，用户需刷新msg-manager页面查看新入口，不宣称HMR或自动刷新。静态字节一致只说明地址可提供该产物，不代表浏览器键盘/读屏已验收。

## 5. 未完成项与安全范围

- 子项待angjustinl正式审前端、非作者正式审脚本及kirinbao原生键盘/读屏/展开布局与交互验收，特别是§3.2的42个跳过场景及§3.3的修正后描述。T08整项仍进行中，SSE/断线重连、真实接入及既有未完成指标不被本次UI收纳替代。MVP **0/27**、扩展 **0/5**、实际完成日期 **—** 不变。
- 未登录或更改任何平台/模型凭据，未读取真实消息、同步/外发、创建计划、迁移数据、启用自动发送、重启后台或部署Telegram。QQ仍仅Milky 1.3，微信只读和飞书独立发送授权/unknown人工核实边界不变。
- 随实现和三份进度记录提交并推送当前功能分支，不建PR、不更新main；真实提交结果在交付回复登记，哈希生成前不编造。
