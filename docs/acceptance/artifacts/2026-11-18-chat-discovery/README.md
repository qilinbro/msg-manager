# CHG-T08-008 / T10-004 工程证据

日期：2026-11-18（Asia/Shanghai）。这里只记录已运行的工程检查，不代替指定人员审查或真实账号/模型/原生 Tauri 验收；实际完成日期仍为 **—**。完整功能与权限边界见[本轮验证](<../../T08-chat-discovery-validation.md>)。当前运行中的后台和客户端没有更新。

## 最终记录

| 项目 | 实际结果 | 原始证据 |
| --- | --- | --- |
| 最终全仓检查 | 退出0，32.192030s；Go/vet、契约、全部前端合成回归、格式/类型/生产构建通过 | [全检日志](<fullcheck.log>) |
| Store/API非缓存race | 退出0，32.337745s；Store25.644s/API23.425s，`-race -tags sqlite_fts5 -count=1` | [race日志](<store-api-race.log>) |
| 最后独立构建 | 退出0，1.209998s；51模块、Vite135ms | [构建日志](<build.log>) |
| 桌面1440×1000 / 窄屏390×844 | 退出0，16.705110s；40项断言、74个已拦截API、0 mutation | [报告](<report.json>)、[浏览器日志](<browser.log>) |
| 原生输入 | 9次鼠标、6次触控、23次键盘；20条原生模态焦点轨迹 | [报告](<report.json>) |
| 冻结指纹 | 完整前端src及相关5个后端/契约输入共43文件、dist3文件；路径集合/SHA-256/bytes/mtime前后一致，harness SHA/bytes一致 | [指纹](<source-manifest.json>)、[冻结runner](<synthetic-browser.mjs>) |

全检中的Go单测命中缓存，不冒称新的非缓存运行；另附单独`-count=1`的race。JS501.98kB超过Vite500kB提示线（gzip148.28kB）为非失败构建提示，没有为此进行范围外拆包。

harness SHA-256：`ea14aaa38a2e680979c5f3b2a1a01a54ca195d5b51cd596487217d094a2eba4f`，37190bytes。[目录属性](<.gitattributes>)保留归档字节，不自动换行转换；仅原始日志保留工具输出的CRLF/报告尾随空白，设定日志级whitespace例外，不放宽源码检查。指纹是当时冻结输入的证据，不保证另一机器的源码换行或mtime与本机相同；复跑会生成新的指纹，不改写本归档。

## 四张最终截图（已逐一查看）

| 截图 | 已检查内容 |
| --- | --- |
| [桌面群聊关键词](<desktop-groups-keyword.png>) | 私聊/群聊和求助入口位于列表滚动区外；正文命中高亮与最近消息、仅名称命中明确分开；白色扁平界面 |
| [390px消息发现](<narrow-groups-keyword.png>) | 标签、关键词、搜索/清除和来源跳转可见，按钮换行，无文档横向溢出 |
| [390px Agent上下文](<narrow-agent-context.png>) | 原草稿加显式追加上下文仍保留，UTF-8字节提示、未勾选的模型同意和关闭按钮可操作；内部/文本框竖向滚动如实保留 |
| [390px微信来源](<narrow-wechat-source.png>) | 标签与名称/ID搜索可见；切换后隐藏peer清空，无匹配提示，读取/监测动作禁用 |

全部为合成数据，不是用户真实会话、模型配置或现有运行实例的截图。没有用截图替代API/存储层测试。

## 原生模态与历史失败

- 原生dialog保持`:modal`；对底层消息标签的程序性focus被拒。18次真实Tab和首尾Tab/Shift+Tab未让任何背景业务控件获焦，7个不同Agent控件实际获焦；关闭恢复表单不进入焦点。
- 20条记录中4次为`BODY`、`page_focused=false`，符合原生浏览器失焦语义，不等于底页业务获焦，也不宣称从DOM记录可读取浏览器工具栏内部状态。最终没有生产focus trap；原生Escape恢复原求助按钮，关闭不丢业务筛选或草稿。
- [首个预检](<failures/LCjLSf/report.json>)：按钮定位错误，0功能断言；[第二预检](<failures/4F62UV/report.json>)：fixture状态路径/target guard错误，0断言/22API/0mutation。
- [第三预检](<failures/HLJK3m/report.json>)：存在性谓词返回DOM，CDP对象链过长，1断言/22API/0mutation；只将谓词改为boolean。
- [首个功能批次](<failures/ZhV3PR/report.json>)：13断言/34API/0mutation，过强的`contains(activeElement)`断言在第3次Tab失败；[原截图](<failures/ZhV3PR/failure.png>)与各失败指纹保留。旧报告未记录具体activeElement，不能据此证明焦点泄漏。参考已安装指南后撤回临时生产补强和第10组测试，校正harness为原生语义再做唯一确认，不伪称严格循环通过。
- [先前全检](<before-tab-cycle-fullcheck.log>)、[先前构建](<before-tab-cycle-build.log>)、[序列化预检输出](<preflight-serialization.log>)及[过强焦点断言输出](<before-tab-cycle-browser.log>)单独留存，不覆盖为成功；阶段文件命名不表示最终包含焦点trap。不继续无界微调/重建/截图循环。

## 安全范围与复跑

使用独立临时profile和独占CDP实例，无应用服务器/监听替换。所有应用HTTP请求均`fulfill`/`fail`，未认识的请求与target一律拒绝；阻止worker/SW/WebSocket/download/window.open等，关闭Chromium常见后台网络功能。报告无console error、interception error、意外请求/target；没有真实应用API、聊天、凭据、模型或平台调用，没有启用监测/计划。

这是应用级拦截，不是操作系统防火墙隔离，不能据此承诺浏览器进程绝对没有任何系统级背景联网。本归档仅复制白名单报告/日志/脚本/截图/指纹，**没有复制profile、cookie、IndexedDB、用户目录或服务凭据**。

已有前端依赖与Chromium的机器，在仓库根目录执行；不需要运行后台，不要提供真实token或账号：

```powershell
npm.cmd --prefix apps/desktop run build
New-Item -ItemType Directory -Path .runtime -Force | Out-Null
# 若默认本机Playwright Chromium不存在，请填写已安装的独立测试Chromium路径：
# $env:SYNTHETIC_CHROMIUM = 'C:\path\to\chrome.exe'
node .\docs\acceptance\artifacts\2026-11-18-chat-discovery\synthetic-browser.mjs --synthetic-only
```

runner以当前工作目录为仓库根目录，从当前dist读取构建，在忽略目录生成新的唯一测试目录。不要修改冻结输入直到本轮结束，不要把新profile纳入归档。实际功能上线仍须另行授权更新后台与客户端，并完成指定非作者审查及kirinbao真实验收。
