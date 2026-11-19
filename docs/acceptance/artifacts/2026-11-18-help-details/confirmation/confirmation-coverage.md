# 唯一确认批：实际覆盖与静态诊断

- **失败 / 验收未完成**；不修改 harness，不新增浏览器，不重试。
- 原始证据：[第二批摘要](<run-ZKuJM8/summary.md>)、[report](<run-ZKuJM8/report.json>)、[trace](<run-ZKuJM8/cdp-trace.jsonl>)、[manifest](<run-ZKuJM8/manifest.json>)；均保持原字节。
- 绑定 2026-11-18 22:34 冻结构建及 manifest 内的源/产物/harness 指纹。根若后续修正源或重建，**这些浏览器截图和报告仍只针对旧冻结版本**，不证明后续版本的浏览器行为。

## 实际结果

- 1440×1000、390×844（touch enabled）均合成连接成功；默认记住设备 initially=true，两次都通过原生输入取消并硬断言 false 后才连接，未登记设备凭据。
- 119 条通过断言、50 条 finding；44 计划场景中只执行 2 个连接/预检综合场景，两者整体 failed，42 个后续场景 skipped（非 42 个产品故障）。
- 每个 viewport 的 8 个 HelpDetails：连接与授权说明、接入说明、核验记录、同步说明、接收说明、模型配置说明、摘要说明、自动化权限说明。共 16 个默认闭合、summary/DOM 内容存在、原生 Tab 到达预期 summary、闭合内容不可见断言通过。
- 两次全页范围 no details inside label 通过。checked 记住设备时共享电脑影响可见；原生取消后影响移除、ARIA 引用切换断言通过。
- 两张截图 metrics 通过：单 root、单 sidebar；inner 1440×1000 / 390×844，DPR=1。CSS client/visual 宽 1425 / 375（原生 15px 滚动条），对应 DOM/CDP 数值一致。390 viewport 的 pageY=213、outer 仍1440×1000是固定初始浏览器窗口+窄viewport emulation的实际记录，不是重复 DOM。
- 14 synthetic GET +14 OPTIONS、8 frozen frontend，共36 HTTP 均被截获；0 forwarding、0业务写入、0真实平台/模型调用；finally 执行且当时源/dist/harness 指纹全部未变。

## 键盘：48 条 finding 不可算产品故障，也不可改成通过

- trace 中 32 个 Enter keyDown 均缺少 text/unmodifiedText；Space 的 32 个 keyDown 均携带空格字符。Shift+Tab 16 次为8，无错误Alt=1。
- 原生 HelpDetails 无自定义键盘阻断。harness key() 仅为 Space 填 text；[Puppeteer 官方 Enter 映射](<https://raw.githubusercontent.com/puppeteer/puppeteer/main/packages/puppeteer-core/src/common/USKeyboardLayout.ts>)使用 `text: '\r'`。缺少 Enter 字符事件使本批原生激活序列无效，与每个 helper 三条反向状态断言失败相符。
- 原始 report 的 16 条“expanded: no horizontal viewport overflow”是在先前 expansion 失败后采集，不得写为展开态通过；16 条“Enter closes; hidden descendants excluded from Tab”只证明最后观测到闭合/焦点不在body，不证明 Enter 造成关闭。所有原始标签不改，解释另附。

## AX：独立的 computed description 关联回归

- 两个 scope 各检查33个可见控件，未报告 computed accessible name 失败；但各有两处 computed description 失败：`service-token → token-description`、`ai-model-preset → ai-model-preset-help`。
- 两目标的 DOM ID 存在、DOM 文本非空；computed 字段在 JSON 缺省，表示 harness 读取的 `ax.description.value` 为 undefined，而非引用 ID 拼错或说明文本为空。
- AX 于原生取消记住及首个披露 Enter 之前采集，因此不能归咎键盘注入缺陷。修正前源关联见 [App:472–518](<../../apps/desktop/src/App.tsx#L472-L518>)、[AIModelPanel:188–208](<../../apps/desktop/src/AIModelPanel.tsx#L188-L208>)；描述目标位于初始closed helper。源行号只表示修正前静态检查定位，后续根修改后可能移动。
- trace 只保留 AX 响应 `result_keys=["nodes"]`，无原始 nodes，不能离线还原 AX 全文或 description sources。
- checked 记住设备 checkbox 同时引用可见 remember-impact 与折叠 remember-description；当前断言只要求 computed description 非空，不能证明两段都完整保留。取消勾选后未再采 AX，该状态未验证。

## 未执行 / 未通过

- Both combined connection/preflight scenarios failed their overall gate despite connected=true; all 42 dependent cases were correctly skipped, not 42 product failures.
- All overview, settings follow-up, Milky busy/success/error, digest states, source states, WeChat stop/refresh/late GET, inbox AI/profile/import, Feishu states and assistant modal cases were not executed.
- No email success/setup/limited/skipped/error acceptance, no WeChat dirty/limited/error/unknown/late-response acceptance, and no model/AI/profile/assistant/Feishu independent-consent acceptance.
- GET-controlled stop/refresh/late-response gate records are empty.
- No valid expanded-helper geometry evidence, no successful full native keyboard sequence, no AX pass for the two checked scopes or the unchecked remember state.
- Connection screenshots are not success evidence for the skipped product views.

## 截图

- 已直接 read_image 审阅两张原图：均单一 app 表面，未见首批758×426平铺；仅连接设置视口，不是展开态或其他页面覆盖。
- [桌面1440](<run-ZKuJM8/connection-1440.png>) · [窄屏390](<run-ZKuJM8/connection-390.png>)。像素周期数值另见 [图片审计](<confirmation-image-audit.json>)。

## 全部未执行场景（保留 blocked-by）

- overview-1440: skipped; blocked by connect-synthetic-only-1440
- settings-1440: skipped; blocked by connect-synthetic-only-1440
- milky-get-busy-success-error-1440: skipped; blocked by connect-synthetic-only-1440
- digest-running-1440: skipped; blocked by connect-synthetic-only-1440
- digest-unknown-1440: skipped; blocked by connect-synthetic-only-1440
- digest-failed-1440: skipped; blocked by connect-synthetic-only-1440
- sources-ready-and-dirty-1440: skipped; blocked by connect-synthetic-only-1440
- wechat-stop-get-refresh-late-1440: skipped; blocked by connect-synthetic-only-1440
- sources-limited-1440: skipped; blocked by connect-synthetic-only-1440
- sources-skipped-1440: skipped; blocked by connect-synthetic-only-1440
- sources-monitor-error-1440: skipped; blocked by connect-synthetic-only-1440
- sources-error-1440: skipped; blocked by connect-synthetic-only-1440
- sources-unconfigured-1440: skipped; blocked by connect-synthetic-only-1440
- inbox-ai-profile-import-1440: skipped; blocked by connect-synthetic-only-1440
- feishu-ready-1440: skipped; blocked by connect-synthetic-only-1440
- feishu-sent-1440: skipped; blocked by connect-synthetic-only-1440
- feishu-running-1440: skipped; blocked by connect-synthetic-only-1440
- feishu-failed-1440: skipped; blocked by connect-synthetic-only-1440
- feishu-report-unknown-1440: skipped; blocked by connect-synthetic-only-1440
- feishu-unknown-1440: skipped; blocked by connect-synthetic-only-1440
- assistant-disclosures-1440: skipped; blocked by connect-synthetic-only-1440
- overview-390: skipped; blocked by connect-synthetic-only-390
- settings-390: skipped; blocked by connect-synthetic-only-390
- milky-get-busy-success-error-390: skipped; blocked by connect-synthetic-only-390
- digest-running-390: skipped; blocked by connect-synthetic-only-390
- digest-unknown-390: skipped; blocked by connect-synthetic-only-390
- digest-failed-390: skipped; blocked by connect-synthetic-only-390
- sources-ready-and-dirty-390: skipped; blocked by connect-synthetic-only-390
- wechat-stop-get-refresh-late-390: skipped; blocked by connect-synthetic-only-390
- sources-limited-390: skipped; blocked by connect-synthetic-only-390
- sources-skipped-390: skipped; blocked by connect-synthetic-only-390
- sources-monitor-error-390: skipped; blocked by connect-synthetic-only-390
- sources-error-390: skipped; blocked by connect-synthetic-only-390
- sources-unconfigured-390: skipped; blocked by connect-synthetic-only-390
- inbox-ai-profile-import-390: skipped; blocked by connect-synthetic-only-390
- feishu-ready-390: skipped; blocked by connect-synthetic-only-390
- feishu-sent-390: skipped; blocked by connect-synthetic-only-390
- feishu-running-390: skipped; blocked by connect-synthetic-only-390
- feishu-failed-390: skipped; blocked by connect-synthetic-only-390
- feishu-report-unknown-390: skipped; blocked by connect-synthetic-only-390
- feishu-unknown-390: skipped; blocked by connect-synthetic-only-390
- assistant-disclosures-390: skipped; blocked by connect-synthetic-only-390

## 详细数据

- [只读分析JSON](<confirmation-analysis.json>)含全部119通过断言分组、AX问题/事件计数、实际42 skipped清单和6份原始证据SHA。原始证据分析前后指纹一致。
- 静态检查另有 [14项harness纯内存回归](<harness-static-validation.json>)；57项fixture GET内存契约通过，但两者均不替代浏览器/真实平台验收。
