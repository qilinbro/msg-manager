# T08 / T04 持久本机连接验证记录

- 日期：2026-11-17（Asia/Shanghai）；CHG-T08-006 / FEAT-T08，关联 CHG-T04-006 / FEAT-T04。
- 状态：实现与最终工程回归通过，子项待审查 / 待验收；指定非作者审查、kirinbao 真实验收与原生 Tauri 验证待进行，T04/T08 整项进行中，实际完成日期 —。
- 职责：joey 负责前端，angjustinl 负责后端/MCP；joey 审后端、angjustinl 审前端。辅助代理审查和合成测试不代替正式审批。
- 设计见 [ADR 0013](<../adr/0013-persistent-local-connections.md>)，使用见 [统一消息中心](<../unified-inbox.md>)，MCP 证据见 [T04 验证](<T04-mcp-validation.md>)。

## 范围与安全基线

1. 浏览器一次输入主令牌后，可明确选择记住此设备 90 天。仅 IndexedDB 不可导出的 P-256 私钥与非秘密 descriptor 持久化；主令牌及 15 分钟短 bearer 不写任何浏览器存储、URL 或日志。
2. 手动/不支持安全持久化时仅当前页面内存连接；刷新需要重新输入。旧版 sessionStorage 主令牌在模块启动时移除，不读取、解析或自动配对；删除被浏览器拒绝时如实提示手动清理站点数据。
3. 服务端持久 grant 位于 SQLCipher v12，最多 32 项；一次性挑战 60 秒、进程内最多 256 项。实际监听地址（包括同端口换 IP）或 master 变化时启动物理清理；A→B→A 不恢复旧授权。
4. 每个设备请求读取 grant；GET/HEAD 至多刷新重试一次，变更不自动重放。临时故障保留私钥，明确失效才重新授权。
5. 忘记先取消恢复/刷新、按旧设备 ID 删除本地 descriptor，再尽力撤销服务端。离线只声称本地已忘记，不谎称远端撤销成功；跨标签页通知不能清除后来生成的新授权。
6. 不访问真实浏览器 profile、剪贴板、凭据、配置、数据库或聊天；不调用真实模型/平台，不停止或接管已有后台。本轮检查只用临时目录、合成数据与隔离浏览器上下文。

## 已执行专项

| 检查 | 实际结果 | 证据与边界 |
|---|---|---|
| `go test -race -tags sqlite_fts5 ./internal/api -run '^TestLocalDevice' -count=2 -timeout 120s` | PASS，5.545s | 9 个测试函数，真实 SQLCipher 临时库及 Go P-256/P1363；下列 API 边界覆盖 |
| `go test -tags sqlite_fts5 ./internal/store -count=1` | PASS，15.323s | 存储实施者执行并反馈；全 store 包，含 v11→v12、现有业务数据保留与迁移回滚 |
| store 本地设备专项 `-race -count=3` | PASS，15.116s | 存储实施者执行并反馈；上下文取消、跨 Store 句柄并发 cap、重开/撤销与严格元数据 |
| `node scripts/check-contract.mjs` | PASS | 新增 4 个 auth operation；仅 2 个 proof operation 匿名，其余保持 Bearer，配对 master-only |
| 浏览器/前端最终专项 | PASS | `node scripts/test-device-auth.mjs` 31 组、`node scripts/test-service-session.mjs` 11 组及 7 个既有业务脚本；真实 WebCrypto、实际 hook/App/Settings 编译与合成行为覆盖，未使用真实后台 |
| 最终 `scripts/check.ps1` | PASS，38.34s，18:34:23 +08:00 结束，退出 0 | 修复后 Go 全包（部分缓存）/vet、契约、bootstrap 9 子用例、启动器 53 次隔离启动、全部前端脚本、Prettier/tsc/Vite；42 modules，最终 JS `index-DcN4daJL.js` |
| `go mod verify` 及扩大 `-race -count=1` | PASS，非缓存 race | cmd/sociald 3.792s、cmd/sociald-mcp 13.077s、API 16.251s、localcredentials 10.111s、MCP 3.906s、store 21.663s、integration 11.512s；精确命令如下 |
| CGO0 MCP 构建与三包回归 / EOF 冒烟 | PASS | localcredentials 7.274s、MCP 0.263s、cmd/sociald-mcp 4.446s；桥接构建成功，EOF 退出 0、stdout/stderr 均 0 字节、未请求后台 |
| 有界隔离浏览器检查 / UI 检测 | PASS，6 项浏览器断言、一次 UI 检测无发现 | 最终构建、Chromium 原生 CDP、全量拦截合成服务；1440×1000 与 390×844，5 张截图已由整合者逐张查看。首轮初始桌面采集伪影经一次等待 compositor 的确认消失，无 UI 返工；详见下文 |

### API 覆盖

测试位于 [设备认证测试](<../../internal/api/local_devices_test.go>)：

- 首次主令牌配对、公开设备 ID 不能作 bearer、device 不能再配对、self-only 撤销、master-any 撤销、撤销后现有 access 立即被拒绝。
- Host/Origin/工作区绑定；缺失/`null` Origin、跨来源、preflight、严格大小写/重复/额外 JSON 字段、尾部内容、错误类型、4 KiB 上限和非 P-256 公钥拒绝。
- 固定 7 行消息、64 字节 P1363、挑战错误绑定/过期/重放、错误签名消耗、并发只有一个成功；256 项上限与清理。
- grant/access 过期及 15 分钟上限；公开 issuer 指纹不能伪造 HMAC；存储取消/关闭返回 503 而不是误报已撤销，临时失败不删除 grant。
- 后台正常重启可复用 grant/access，不恢复旧 challenge；master A→B→A、端口 A→B→A、同端口 IPv4 `127.0.0.1→127.0.0.2→127.0.0.1` 和 IPv6 `127.0.0.1→::1→127.0.0.1` 不复活旧授权。
- 非规范 listener 不宣传设备持久化，仍可用 legacy master API；设备 auth 在 AI/model 配置锁持有时仍可完成。

### 辅助安全复核

一次独立只读辅助复核限于 API/store/migration。发现初版将服务来源固定为 `127.0.0.1:<port>`，未区分真实监听 IP，存在同端口换地址后旧 grant 仍有效的证明中继路径。已将真实 `cfg.Addr` 纳入 issuer、保持启动物理 prune，并明确非规范 listener 只保留 master API；新增上述 IPv4/IPv6 A→B→A 回归后 race 两轮通过。最终源码复核无剩余可操作发现。

覆盖限制：没有真实跨用户代理复现、崩溃故障注入、真实凭据或前端/MCP/PowerShell 审查；后者由整合检查另记。该结果不是 joey 的正式后端批准，也不代表产品安全证明。

### 整合辅助复核与返工闭环

一次普通只读辅助复核另发现 3 项 P2，均已修复并纳入最终版本检查：

1. 重复实例可能在 bind 失败前先 prune 活跃 grant：改为先绑定监听端口再构造 handler；[启动测试](<../../cmd/sociald/main_test.go>) 验证占用端口不改变已有授权，构造失败释放端口。
2. sessions 401 可能只是挑战到期/后台重启丢缓存，不能删除长期密钥：最多重取一次 challenge/proof；重复 proof 401 或 generic challenge 401 返回可恢复 503，明确 `DEVICE_UNAUTHORIZED` 才失效。另补 GET/HEAD 唯一业务重试仍 401 时保留密钥，不再重试；403 不刷新。
3. 实际 hook 在更换配对失败后的手动 fallback 可能删旧 descriptor：失败保留旧授权并尽力撤销已知新 grant，只有明确取消记住才退役旧授权；覆盖 4 类失败及其他标签页继续使用旧身份。

设备脚本从首轮 24 组增至最终 31 组，新增真实 hook、虚拟 +61 秒 TTL、重启丢 challenge、重复/模糊 401、第二次明确撤销及唯一 read retry 边界。没有因此增加业务 mutation 重放。AI 辅助结论不是指定非作者审批。

扩大回归实际命令（临时 SQLCipher、合成 token、隔离环境变量；已配置本机 Go/GCC 和 `CGO_ENABLED=1`）：

```powershell
go mod verify
go test -race -tags sqlite_fts5 ./cmd/sociald ./cmd/sociald-mcp ./internal/api ./internal/localcredentials/... ./internal/mcp ./internal/store ./tests/integration -count=1 -timeout 240s
.\scripts\check.ps1
```

### 有界隔离浏览器证据

- 使用本机已安装 Chromium、新建临时 profile，原生 CDP 对最终 dist 的前端及所有后台 URL 作拦截响应，不运行/连接真实后台。Node P-256 验签核对实际浏览器 WebCrypto 的 P1363 签名；没有真实凭据、聊天、模型、平台或业务写入。
- 桌面 1440×1000、窄屏 390×844：密码输入、默认 90 天选项、键盘焦点顺序与无横向溢出；实际 IndexedDB 私钥 `extractable=false`、字段仅 descriptor/密钥，主令牌草稿清空且 Web Storage 无 bearer。
- 刷新复用同一设备密钥并新签会话，不再主令牌注册；离线忘记删除本地密钥且明确远端撤销未确认，刷新不复活；在线忘记仅确认一次 DELETE。
- 最终确认结果为 PASS：6 项断言、2 次合成 enrollment、3 次签名 session、1 次远端撤销，零业务 mutation / 未处理页面异常 / 未预期页面网络请求。
- 本地忽略目录证据：`.runtime/persistent-browser-1794911739093/result.json` 及同目录 5 张截图。初轮 `.runtime/persistent-browser-1794911603717` 的初始桌面截图有 tiled/compositor 采集伪影，未据此声称视觉通过；仅修正采集等待 fonts/两帧后进行一次确认，5 张最终截图均已查看，产品代码无追加视觉改动，不再扩展设计循环。
- 这不是实际后端进程重启、90 天实等、真实 MCP 客户端、真实用户浏览器 profile 或原生 Tauri 验收；后端重开/到期/轮换另由 fake-clock 与临时 SQLCipher 测试覆盖。临时浏览器证据不替代下列人工清单。

## 待 kirinbao 组织的真实操作验收

以下均为待执行清单，不是完成记录。使用本人明确授权的本机服务；任何真实聊天/模型调用另行确认。

- [ ] 正常停止旧后台、更新构建，以启动器显式 `-CopyApiToken` 启动；确认只有这次复制，不输出主令牌。工作台默认记住设备，首次输入后进入消息。
- [ ] 刷新、关闭重开同一浏览器来源、另开同源标签页及正常重启后台：无需再复制令牌；只恢复连接/GET 状态，不重放导入、同步或分析。检查草稿、联系人选择和页面没有因短 bearer 刷新重挂。
- [ ] 前端 origin 改变（如 localhost 与 127.0.0.1 互换）、站点数据删除、浏览器私密/受限模式时提示准确，不擅自跨来源迁移私钥。
- [ ] 不勾选记住或禁用安全持久化时，确认当前页面可连接；刷新后重新输入。旧 sessionStorage 不再保留主令牌；清理被拒时提示手动清站点。
- [ ] 临时断网/后台离线/503 后恢复，保留设备身份并重新连上；多个并发读取只进行一次刷新，403 不触发刷新。
- [ ] 在变更请求发出后制造过期 401，确认不自动再次提交，明确要求用户核对后重试。网络未知不能写成失败后自动补发。
- [ ] 在线忘记：本地断开、服务端 grant 撤销、其他标签页收到失效通知；迟到响应不复活连接，不删除之后新配对的 grant。
- [ ] 离线忘记：显示远端撤销未确认；说明其他已打开页面短令牌及未知 orphan grant 的限制。必要时由操作人停后台后显式轮换全部。
- [ ] 轮换主令牌或实际后台地址后，旧浏览器授权不能复活；重新配对后才恢复。MCP 重启读取新受保护 token，显式 env 客户端自行安全更新。
- [ ] 验证电脑/后台必须运行的提示；不误认已安装 Windows 服务、关机仍在线或平台自动发送。
- [ ] joey 后端、angjustinl 前端正式审查与 kirinbao 浏览器/原生 Tauri 验收登记实际日期、版本及证据。

## 完成口径

工程检查通过后可标“实现待审查”，不能标任务整体已完成；T04/T08 实际完成日期保持 —，MVP 0/27、扩展 0/5 不变。不得把新记录覆盖成历史 CHG-T08-004 会话方案的当时结果；历史说明见 [原验证记录](<T08-service-session-validation.md>)。
