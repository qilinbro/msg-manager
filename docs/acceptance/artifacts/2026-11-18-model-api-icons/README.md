# 模型 API 与图标批次原始证据

日期：2026-11-18（Asia/Shanghai）；CHG-T04-007 / CHG-T08-009 / CHG-T11-005。

- [完整说明与待验收边界](<../../T04-T08-T11-model-api-icons-validation.md>)。
- [最终确认](<confirmation/report.json>)：43项检查、52个拦截请求（含CORS预检）、0 mutation、0真实模型/平台调用，7图；50源文件、5个dist文件及harness指纹批内不变。
- [完整初始inspection](<initial-inspection/report.json>)：39项检查、50请求、18条局部尺寸违规；随后仅一个CSS/回归修复批次和一次最终浏览器确认。
- [首次尺寸早停](<first-target-stop/report.json>)、[metadata计数早停](<metadata-counter-stop/report.json>)及各自harness/截图保留；后者是测试把OPTIONS算入GET计数的错误。
- [最终全检](<logs/model-api-icons-check-20261118-172550.log>)、[非缓存race](<logs/model-api-icons-race-20261118-171204.log>)，其余失败和被后续结果替代的日志一并保留。
- [归档索引](<archive-index.json>)记录原目录、结果和每个复制文件的SHA-256；[完整校验和](<checksums.sha256>)另含索引和本说明，不包含校验和自身。

仅拷贝合成证据，未归档Chromium profile、真实账号、聊天、凭据或dist包。本轮没有启动后台/Web服务，没有部署/重启现有实例；业务GET携合成bearer，CORS OPTIONS由拦截器处理，不以预检证明业务鉴权。原生select通过DOM change设置，不宣称覆盖了系统下拉框键盘操作；原生dialog允许焦点进入浏览器chrome/文档容器，但不得进入背后的业务控件。截图中的Agent目录3项为合成fixture，不代表实际后端73项业务目录。
