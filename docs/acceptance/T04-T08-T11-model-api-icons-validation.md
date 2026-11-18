# T04 / T08 / T11｜模型 API、业务检索与图标优先界面验证

日期：2026-11-18（Asia/Shanghai）。关联 FEAT-T04 / CHG-T04-007、FEAT-T08 / CHG-T08-009、FEAT-T11 / CHG-T11-005。

**状态：实现及合成工程验证通过，待指定非作者审查 / 真实验收；实际完成日期—。** MVP 0/27、扩展0/5不变。仅交付 `feat/mcp-service`；不建PR、不更新main、不部署、不重启或替换现有后台/客户端。

## 1. 交付与来源

- [模型模板 API](<../../internal/api/model_presets.go>)：鉴权 `GET /api/v1/ai/model-presets`，6个静态模板，`Cache-Control: no-store`，拒绝查询参数；没有账号状态、模型ID、密钥、探测或自动模型发现。
- [官方来源与配置说明](<../model-provider-presets.md>)记录OpenAI、DeepSeek、Gemini、DashScope及自定义/loopback模板、固定GitHub提交、参考许可与精确地址。仅参考公开配置，没有复制这些项目的SDK代码，也未验证真实账号/模型。
- [模型目标校验](<../../internal/agent/config.go>)仅为官方固定HTTPS主机/精确路径增加Gemini和DashScope的Chat Completions兼容；原root或`/v1`规则、公网DNS/TLS、固定拨号、无代理/重定向与超时/额度边界保持。原生Anthropic Messages、OpenAI Responses、原生Gemini未实现。
- [Assistant目录](<../../internal/api/assistant_catalog.go>)覆盖73项业务API：53项可执行（51业务+2健康）、7项仅人工、13项未实现；另有5项助手控制。新增无参数只读 `get_model_presets`，不把凭据写入或未实现接口交给模型，不开放任意HTTP/SQL/文件/操作系统能力。
- [外部MCP](<../../internal/mcp/server.go>)仍是6工具，不等同于原生Assistant完整目录。会话搜索补充严格可选 `private/group`，两种检索补齐正文 `q`、字节边界、重复/未知参数拒绝、固定路径及分页转发；REST继续执行作用域/查询/版本绑定游标及暂停保护。
- [共享Icon组件](<../../apps/desktop/src/Icon.tsx>)以Lucide1.75.0固定提交的49个原始SVG构成50个别名；静态回归覆盖31个字面量图标用名，动态导航/状态别名另计；[来源清单](<../../third_party/lucide/manifest.json>)逐个记录SHA-256，[完整许可](<../../third_party/lucide/LICENSE>)包含ISC与Feather来源MIT。许可及来源随Web本地构建输出，无CDN、品牌图片、装饰图或新增运行时依赖。
- 导航、筛选、读取、保存、Agent等采用图标及短标签；熟悉的次操作可仅图标，但保留准确 `aria-label` / `title`、焦点和至少44px目标。装饰SVG为 `aria-hidden=true` / `focusable=false`。白底、细边框、无渐变/玻璃/阴影保持。

## 2. 配置、授权与隐私边界

[模型设置组件](<../../apps/desktop/src/AIModelPanel.tsx>)与[真实类型化客户端](<../../apps/desktop/src/model-config-api.ts>)独立读取鉴权模板；元数据失败/迟到不阻断或覆盖人工配置。选模板只填地址并清空模型ID、临时密钥与保存勾选；模型ID需本人核对控制台。改地址、模型、密钥、额度均撤回同意，不自动保存、探测或调用模型；来源链接固定GitHub提交且无opener/referrer。

[配置保存](<../../internal/api/model_config.go>)保留CAS、密钥只写/加密和目标版本撤权。空密钥仅在原始 `base_url` 逐字相同时保留旧值；不同公网HTTPS目标必须提供新密钥。换成完整校验通过、显式端口的数字HTTP loopback且留空密钥时，清除旧云端密钥，绝不继承；非法URL/模型/版本冲突不改变存储、同意或计划。该本机模板不保证所装服务或模型支持原生tools。

必要说明仍可见：来源以实际配置和入库为准；本页不发聊天/邮件；模型目标、费用、共享额度和独立授权；冻结写操作逐项确认；unknown只读恢复、不自动重发；关闭窗口/停止等待不等于取消后台运行。原生 `dialog.showModal()`、设备授权、8000 UTF-8字节草稿边界与显式上下文转入不变。

通用会话发送、通用任务/审批/发件箱未因此实现。飞书本人早晚报仍是默认关闭、固定本人收件人/时间窗的独立外发授权；accepted不等于手机收到/已读。未启用真实计划。

## 3. 工程检查

| 检查 | 结果与可追溯证据 |
|---|---|
| 最终完整检查 | [原始日志](<artifacts/2026-11-18-model-api-icons/logs/model-api-icons-check-20261118-172550.log>)：`scripts/check.ps1` PASS，50.2039992s；Go格式/全仓test/vet、契约、9项bootstrap、53次隔离launcher、全部JS回归、Prettier、TypeScript、Vite构建通过。普通Go test部分命中缓存，不宣称全仓非缓存。 |
| 扩大非缓存race | [原始日志](<artifacts/2026-11-18-model-api-icons/logs/model-api-icons-race-20261118-171204.log>)：`go test -race -count=1 -tags sqlite_fts5 ./internal/agent ./internal/api ./internal/mcp ./internal/store` PASS；模块分别4.105s / 24.129s / 2.469s / 24.651s。此后仅UI/测试/文档改动，无Go改动。 |
| 兼容wire/TLS | [实际Provider测试](<../../internal/agent/compatibility_test.go>)通过本地合成TLS服务器验证官方Host/SNI/路径/Authorization、两轮原生tool call/result对应；保持证书验证/公网DNS判定/固定拨号，混合公网和loopback DNS答案在拨号前拒绝。不是联系真实供应商。 |
| 模板与目录 | [API/Assistant测试](<../../internal/api/model_presets_test.go>)覆盖鉴权、静态来源、无密钥/无状态修改、拒绝查询/写入及两轮实际dispatcher；[配置测试](<../../internal/api/model_config_test.go>)补充云→无密钥IPv4/IPv6、同URL留空保留、31个非法目标/模型、SQLCipher重开后的密钥清除和同意撤销。 |
| MCP | [实际SDK/转发测试](<../../internal/mcp/chat_search_test.go>)覆盖6工具schema、可选枚举、中文字面正文、`%`/`_`、256 UTF-8字节边界、空白、重复/未知字段、鉴权路径及游标。MCP额外拒绝控制字符；不改变REST既有字面匹配约定。 |
| 实际前端组件 | [模型设置回归](<../../scripts/test-ai-model-client.mjs>)、[模板客户端回归](<../../scripts/test-model-presets-client.mjs>)、[图标回归](<../../scripts/test-ui-icons.mjs>)及既有业务组件回归通过；VM渲染[真实Icon](<../../scripts/frontend-icon-test-loader.mjs>)，不以空占位图标规避名称/几何断言。 |
| 工具链差异 | [设备回归](<../../scripts/test-device-auth.mjs>)仅兼容实际DOMException的标准 `InvalidAccessError` 与Node24.12旧名 `InvalidAccessException`；两种本机Node环境各31组通过，不改变设备密钥安全语义。 |

最终Vite构建52模块，188ms；CSS 59.06KB、JS 517.06KB。保留Node类型擦除实验性与JS chunk>500KB非阻断警告，不为消除警告扩展本轮范围。环境为Windows Go1.27.4/GCC14.2/CGO+SQLCipher+FTS5、Node24.21.0；浏览器为本机隔离headless Chromium。

## 4. 失败、返工与唯一最终浏览器确认

原始证据只拷贝不改写，[归档索引](<artifacts/2026-11-18-model-api-icons/archive-index.json>)、[校验和](<artifacts/2026-11-18-model-api-icons/checksums.sha256>)与[说明](<artifacts/2026-11-18-model-api-icons/README.md>)包含44个文件，不含Chromium profile。为保留Vite原始终端空格与错误栈末尾空行，仅[该批日志属性](<../../.gitattributes>)定域豁免这两项空白检查，其余检查不变。所有轮次均0 mutation / 0真实模型平台调用，不删除或淡化失败记录。

1. [首轮早停](<artifacts/2026-11-18-model-api-icons/first-target-stop/report.json>)：1条检查/14请求，发现旧profile规则覆盖全局44px，实际39.5px；保留当时harness和失败图。随后仅调整inspection收集方式，让尺寸失败汇总而非掩盖其他视图。
2. [metadata计数早停](<artifacts/2026-11-18-model-api-icons/metadata-counter-stop/report.json>)：7条检查/16请求。测试把CORS OPTIONS也计入模板GET计数，显式刷新实际加2却期待加1导致超时。改为仅统计GET，是harness缺陷，不是产品自动保存/外呼；保留旧harness与日志。
3. [完整初始inspection](<artifacts/2026-11-18-model-api-icons/initial-inspection/report.json>)：39条通过检查/50请求，最终严格断言失败，汇总18条尺寸违规：桌面及860px每日整理按钮39.5px；桌面收件箱及Agent背景中的4种视图筛选41.796875px、2种后台读取按钮42px。7张正常截图全部查看，另存failure图。
4. 只做一个UI修复批次：[profile局部覆盖](<../../apps/desktop/src/profile.css>)、[收件箱基础/窄屏/监控覆盖](<../../apps/desktop/src/inbox.css>)统一相关按钮最小44px；[静态回归](<../../scripts/test-ui-icons.mjs>)检查指定selector的全部显式px覆盖，防媒体查询回退。未改导入输入框或添加无关装饰。
5. 最终全检构建后，使用完整初始inspection的同一harness，**仅一次最终确认**：[报告](<artifacts/2026-11-18-model-api-icons/confirmation/report.json>) PASS，43条检查、52个拦截API请求、0 mutation、0真实模型/平台调用、0图标违规，7张截图逐一查看。没有继续润色或再开浏览器循环。

### 最终确认覆盖与限制

- 1440×1000桌面、860×1000中间宽度、390×844窄屏；模板/来源可换行、不横向溢出；可见图标按钮准确名称/hover名称/装饰SVG及≥44px目标通过。
- 模板只填地址、手动配置不被元数据刷新覆盖、目标变更不串密钥/模型、额度变更撤回同意、默认未勾选/保存禁用、loopback仅模板状态。
- 群聊+字面关键词“交付”、正文命中、完整上下文求助名称、显式草稿转入、费用与独立同意、Escape恢复触发控件/会话/检索。
- 实际原生modal、16次原生Tab和Escape、2次原生touch输入。焦点不得进入背后业务控件；允许浏览器chrome/文档容器，并要求访问多个不同modal控件，不用自制trap伪造通过。模板select通过DOM value/change设置，不声称覆盖了操作系统原生下拉框键盘选择。
- 所有应用HTTP请求由CDP拦截合成响应或失败关闭；业务GET带合成bearer，OPTIONS预检单独拦截，52次包含预检。无后台/Web服务、真实配置或实际应用实例；没有应用实际供应商/平台外呼，也没有通过真实DNS或账号验证兼容性。
- 截图中的3项目录为合成fixture，不能当作实际73项目录或真实服务证据；实际目录及权限数量由Go/契约检查验证。
- [源/构建清单](<artifacts/2026-11-18-model-api-icons/confirmation/source-manifest.json>)：50个源文件、5个dist文件的内容/mtime及harness内容指纹在批内不变，归档前再次逐个校验。最终harness SHA-256：`0e7602046022c5408366226613b3106a74af3ba5144b4e454a7b13fa6032222f`。

### 最终截图

| 视图 | 截图 |
|---|---|
| 桌面模型模板 | [1440px](<artifacts/2026-11-18-model-api-icons/confirmation/desktop-model-presets.png>) |
| 窄屏模型模板 | [390px](<artifacts/2026-11-18-model-api-icons/confirmation/narrow-model-presets.png>) |
| 窄屏保存/计划同意 | [390px](<artifacts/2026-11-18-model-api-icons/confirmation/narrow-model-confirmation.png>) |
| 桌面收件箱 | [1440px](<artifacts/2026-11-18-model-api-icons/confirmation/desktop-inbox-icons.png>) |
| 桌面Agent | [1440px](<artifacts/2026-11-18-model-api-icons/confirmation/desktop-agent-icons.png>) |
| 窄屏Agent | [390px](<artifacts/2026-11-18-model-api-icons/confirmation/narrow-agent-icons.png>) |
| 窄屏收件箱 | [390px](<artifacts/2026-11-18-model-api-icons/confirmation/narrow-inbox-icons.png>) |

更早全检历史保留于同一归档：16:54设备异常名失败、16:56阶段通过、17:12新增披露后前端格式失败、17:15格式修正后通过、17:25尺寸返工后最终通过；后一个通过不抹去前述失败。初次与最新race均保留。没有重跑或改写前轮chat-discovery/飞书证据。

## 5. 审查、验收与下一步

- [x] 两名普通AI只读辅助复核后端与前端；后端未报具体阻断，前端发现并复核上述局部尺寸修复，无新静态阻断。另一次资料/证据只读辅助复核更正图标计数口径，并核对归档、50源/5构建及失败历史，无剩余具体事实阻断。均不代表指定人员批准，也未声称调用指定安全审查流程。
- [ ] joey正式审查后端：精确兼容路径、凭据目标迁移、模板无外呼、Assistant能力与MCP参数；angjustinl正式审查前端：图标/名称/尺寸、配置状态/同意、原生modal和设备/草稿回归。
- [ ] 脚本由非作者技术成员正式审查，kirinbao组织交互/产品验收；指定人员实际批准与日期均待定。
- [ ] kirinbao另行明确授权更新现有运行实例后，使用本人选择的模型ID/凭据验收真实服务、原生tools/费用/错误及模型变更撤权；另行验收真实MCP客户端、微信/邮箱/QQ/飞书和原生Tauri。合成拦截不是集成或真实账号验收。
- [ ] T08 SSE/断点重连、T10固定查询Recall@5及通用任务/审批/发送等既有整项缺口继续推进，不由本轮替代。

三处进度：[任务卡](<../../任务规划.md>)、[feat登记](<../../feat.md>)、[技术文档§43](<../../技术文档.md>)同次提交。上传不等于验收；所有实际完成日期保持—。
