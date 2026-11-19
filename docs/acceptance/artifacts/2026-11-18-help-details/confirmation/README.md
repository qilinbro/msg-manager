# 唯一修正确认批：失败证据归档

本目录记录2026-11-18的唯一修正确认批，绑定当日22:34冻结产物。**不是2026-11-19描述关联修正后的浏览器结果，也不是全项目验收通过。** 当前实现、修正后工程结果和全部待验边界以[主验证记录](<../../../T08-help-details-validation.md>)为准。

- [报告](<run-ZKuJM8/report.json>)、[原始摘要](<run-ZKuJM8/summary.md>)、[指纹清单](<run-ZKuJM8/manifest.json>)、[CDP轨迹](<run-ZKuJM8/cdp-trace.jsonl>)与[1440桌面图](<run-ZKuJM8/connection-1440.png>)/[390窄屏图](<run-ZKuJM8/connection-390.png>)为原始6份输出，未修改或重写失败结果。
- [分析](<confirmation-analysis.json>)、[实际覆盖](<confirmation-coverage.md>)和[图片核查](<confirmation-image-audit.json>)是事后无浏览器/无网络分析；覆盖说明中的原源码路径/行号依赖原运行层级，部分相对链接不适用于归档目录，请从主验证记录导航当前源码。
- [修订harness](<synthetic-browser.mjs>)、[相对初版差异](<harness-revision-1.diff>)、[静态harness检查](<harness-static-validation.json>)、[fixture](<fixtures.mjs>)、[fixture契约检查](<validate-fixtures.mjs>)及[原运行说明](<harness-README.md>)为审计快照。14项纯内存harness检查及57项fixture GET契约通过，不等于浏览器通过。
- [复制校验](<copy-verification.json>)记录15份文件在复制前、归档后与源复核的SHA一致；首批证据仍在父目录，不覆盖首报。不包含Chromium profile、缓存、密钥、个人配置或实际聊天。

## 结果限定

119条通过断言、50条finding；44个计划场景仅执行2个连接/预检综合场景且整体failed，0场景passed、42 skipped。48条键盘序列finding存在Enter字符注入缺失，未再运行浏览器作因果复验；不能直接归为产品缺陷或改记通过。另2条独立AX范围finding保留，后续已作10处关联源码修正，但修正后computed AX/原生键盘/展开态布局仍待验。

第二批14个合成GET、14个OPTIONS及8个冻结静态请求均拦截，0转发/业务写入/真实模型或平台请求；最终指纹核对执行。两张原图仅连接视口，不覆盖跳过场景或修正后UI。

## 不可直接运行

脚本以自身目录上两级定位仓库，归档目录层级不匹配。没有另行授权不得复跑；即使授权，也须恢复原运行深度并使用匹配的已审核旧构建，不能将旧批次与新产物混作复现。此归档不构成追加浏览器循环、真实账号操作或业务外发的授权。
