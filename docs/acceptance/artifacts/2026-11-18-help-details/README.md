# T08 非必要说明折叠：隔离合成浏览器验证

仅检查最终生产前端的展示、原生键盘交互、可访问名称/描述、移动触摸与横向布局。不代表真实 QQ、微信、邮箱、飞书或模型调用验收。

## 安全边界

- 仅在父代理确认最终构建与源文件冻结后运行。脚本要求明确的 `--synthetic-only`、`--approved-final-build` 和本 checkout 内的绝对 `--dist` 路径。
- 不启动、重启或替代现有服务器。逻辑页面 origin 为 `http://127.0.0.1:1420`，但所有前端响应均由 CDP `Fetch.fulfillRequest` 从已冻结在内存的最终 dist 字节生成；没有 `Fetch.continueRequest`，也没有应用 HTTP 客户端转发。
- 业务 API 仅允许 GET 与其 OPTIONS 预检，响应完全来自内存合成 fixture。未知路由和所有写方法 fail closed；不点击任何业务保存、授权提交、模型执行、读取并保存消息、同步邮箱、生成日报或发送按钮。
- Chromium 使用本证据目录内全新独立 profile，绝不读取个人 profile、真实凭据或系统剪贴板。记住设备保留产品原始默认值，但脚本先用原生点击取消勾选，并用硬断言确认 false 后才输入合成 token 和连接；不执行设备登记 POST。
- 禁用缓存、Service Worker、页面 WebSocket/EventSource、workers、RTC、弹窗、下载与剪贴板。额外 target 在启动处暂停并使整批失败。
- 通过受控暂停 GET 验证 busy/停止等待：微信先停止等待、GET 更新状态清除 uncertain、保留 warning，再释放迟到的会话 GET，检查旧响应不会回填或覆盖。
- 原生 details 只用 Enter/Space 开关、Tab 验证顺序；不通过修改 `.open` 或 React state 伪造状态。仅合成本地输入/选择会产生脏配置草稿，绝不提交。
- 开始与结束核对全部 src、dist 的 SHA-256/mtime，以及执行中的两个 harness 文件 SHA-256。源、产物或 harness 改变会失败。

## 运行前纯内存检查

以下命令不启动浏览器，不连接服务：

```powershell
# cwd: D:\msg_manager\.runtime\main-push-20261116\repo
& 'C:\Users\MECHREVO\.dsh\dsh-runtimes\dsh-primary-runtime\dependencies\node\bin\node.exe' --check '.runtime/T08-helper-details-20261118/synthetic-browser.mjs'
& 'C:\Users\MECHREVO\.dsh\dsh-runtimes\dsh-primary-runtime\dependencies\node\bin\node.exe' --experimental-vm-modules '.runtime/T08-helper-details-20261118/validate-fixtures.mjs'
```

最近纯内存检查通过 57 个 GET fixture 契约；此检查不代表浏览器结果。

## 经明确批准后的一次桌面+移动合并批

```powershell
# 只有父代理最终构建标记后才可执行；不自行重试、循环或启动服务器。
# cwd: D:\msg_manager\.runtime\main-push-20261116\repo
& 'C:\Users\MECHREVO\.dsh\dsh-runtimes\dsh-primary-runtime\dependencies\node\bin\node.exe' '.runtime/T08-helper-details-20261118/synthetic-browser.mjs' --synthetic-only --approved-final-build '--dist=D:\msg_manager\.runtime\main-push-20261116\repo\apps\desktop\dist'
```

一批包含 1440×1000 与 390×844；后者启用触摸输入。第一批集中收集问题；仅经父代理批准，至多一次合并修正后的必要确认批，不扩成循环试错。

## 覆盖

- 概览、连接、模型、Milky、每日摘要、来源、导入、会话、AI、档案、飞书与助手的说明/成功记录默认折叠，原生 Enter/Space 恢复原内容，闭合后不进入隐藏控件。
- 通过 Chromium AX tree 检查可见控件名称，以及仍可引用折叠区文本的 `aria-describedby` 描述；禁止 label 内嵌 details。
- 390 与桌面的默认/展开态横向布局；模型、摘要、AI、档案、飞书、助手已有独立费用/数据/发送同意仍未勾选且在相应操作旁。
- 微信未选择时静态说明隐藏，选择后数据/费用影响可见，后台本地名单脏草稿、limited、monitor error、GET error、停止等待 warning 与来源“需检查”保留。
- 邮箱成功同步记录隐藏，未配置说明可展开，limited/error 保留；skipped=1 且 limited=false 时不完整警告和来源“需检查”直接可见，而成功收件数量仍折叠。
- 摘要实际正文与提醒直接展示；仅成功元数据折叠。running/failed/unknown 明示。飞书已冻结正文不隐藏，合成成功回执细节折叠但关键平台状态保留；unknown 不重发。
- 助手更多选项可通过键盘展开，非空草稿的独立同意与费用可见，模态 Tab 约束、Escape 与重开草稿保留。

## 证据

每批仅写入本目录新的 `run-*` 子目录：报告、SHA 清单、逐条 CDP JSONL trace、PNG 截图、简报以及独立 Chromium profile。trace 省略授权、请求正文与响应资产正文，响应只记录顶层 key，完整断言与失败上下文在报告中。没有真实平台/模型数据。证据不替代指定非作者审查与 kirinbao 最终验收；正式任务卡和三文档登记由父代理负责。
