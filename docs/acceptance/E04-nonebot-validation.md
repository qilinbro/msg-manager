# E04 / T22：实际 NoneBot 适配器工程验证

- 日期：2026-11-19（Asia/Shanghai）；FEAT-E04 / CHG-E04-002、FEAT-T22 / CHG-T22-002。
- 分支：`feat/mcp-service`；初始基线：`ab0675da701db39136bfae0c10fd376567de0d6e`；最终验证基线：`b9420cbab45104280d49ab463a58a8bd3c44a5bd`（独立提交的并行微信修复，保留为父提交）。本轮提交生成前不填写新哈希。
- 状态：**受限接入及闲置游标P1修复的合成工程检查通过；指定非作者审查、kirinbao验收未完成，实际完成日期 `—`**。AI辅助审查与脚本通过不等于正式批准。
- 既有[全功能预检](<T22-pretest-validation.md>)及其首轮失败/复测保留；本轮不覆盖历史，不把[FT-001—070](<T22-all-features-test-plan.md>)或M4标成通过。

## 1. 实际交付和边界

采用用户指定的 [NoneBot Telegram](https://github.com/nonebot/adapter-telegram) 与 [NoneBot飞书](https://github.com/nonebot/adapter-feishu) Python包，不是只改名称，也不是部署NoneBot服务替代本产品。

- Python固定入口：[bridge.py](<../../integrations/nonebot_bridge/bridge.py>)，仅 `telegram.probe`、`telegram.poll`、`feishu.send`；固定 `python -I -u -B`，经stdio与Go交换有界JSON。未启动HTTP监听器、插件、NoneBot生命周期、`.env`或后台轮询。**stdio及精简环境不是OS沙箱**，解释器/脚本仍是本机可信代码。
- Telegram：[Python适配器](<../../integrations/nonebot_bridge/telegram.py>)、[Go客户端](<../../internal/connector/telegrambot/telegram.go>)、[API](<../../internal/api/telegram_bot.go>)、[原子持久化](<../../internal/store/telegram_bot.go>)、[迁移15](<../../migrations/0015_telegram_bot.sql>)。仅Bot API，最多20名授权私聊、每次新鲜人工同意、单页最多100事件、纯文本最多16000 UTF-8字节。核验/每页均检查预期Bot身份与webhook；已有webhook拒绝而不删除。消息/最小事件/来源/去重/游标/epoch/revision原子提交，下次显式接收才使用新offset。不发送、不读个人号历史、不自动AI。
- 飞书：[Python适配器](<../../integrations/nonebot_bridge/feishu.py>)与[Go NoneBot sender](<../../internal/connector/feishu/nonebot.go>)进入现有固定本人冻结摘要链路。默认传输始终NoneBot；缺runtime在一次性claim前拒绝，无自动legacy fallback；仅显式 `legacy-go` 兼容。历史回执可在缺runtime时只读恢复、不重发。固定OAuth/消息端点、self open_id、正文、UUID、最小scope；最多1次token+1次消息，无代理/重定向/刷新重放/自动重试。不确定为 `unknown`；明确 `21001/not_sent` 撤销相同绑定周期授权。换适配器不消除平台数据安全策略，也不证明手机送达/已读。
- 前端：[受限客户端](<../../apps/desktop/src/telegram-bot-api.ts>)、[手动面板](<../../apps/desktop/src/TelegramBotPanel.tsx>)、[设置接入](<../../apps/desktop/src/App.tsx>)。每次接收重新勾选；失败、忙碌、取消、令牌切换/卸载/迟到响应不复用同意；显示Bot来源及“仅手动收件”。无新增AI、导航、后台刷新或重试。
- 配置与限制见[接入说明](<../../integrations/nonebot_bridge/README.md>)和[第三方声明](<../../integrations/nonebot_bridge/THIRD_PARTY_NOTICES.md>)。固定Windows x64 / CPython3.12，[34项SHA256锁](<../../integrations/nonebot_bridge/requirements-windows-py312.lock.txt>)；[准备脚本](<../../scripts/prepare-nonebot-bridge.ps1>)默认只检查，显式安装仅新/空目标，不升级或重装现有环境。

## 2. Telegram闲置恢复P1：复现、修复与局限

[官方Update说明](https://core.telegram.org/bots/api#update)允许一周无新更新后随机选择新的update_id；[接收队列](https://core.telegram.org/bots/api#getting-updates)至多保留24小时，[getUpdates](https://core.telegram.org/bots/api#getupdates)的offset具有确认语义。

- 复现：旧ID推进至900000001；以临时库时间模拟8天闲置，新ID回到200000000。旧offset路径被严格客户端拒绝，重新核验/重启本身不能修复；实际SDK配合假HTTP使用offset=0可读取并推进至200000001。确认的是恢复缺陷，**未证实真实平台已提前ACK或静默丢失**。
- 修复：store按最后**非空页成功提交**时间持有24小时租期；缺失/非法/未来/到期时，在唯一一次平台请求**前**选择offset=0。空页不续期；仍按实际请求offset严格验证返回ID，不自动二次读取、重试或读历史。
- 重置原子旋转epoch；事件按epoch去重，消息按bot/chat/message稳定键跨epoch去重。精确快照与递增revision CAS拒绝过期页面，即使数值offset回到相同值（ABA）；冲突、回滚、取消、溢出无部分写入。读/置顶/归档及AI默认关闭保持不变；重启/重新核验不丢这些状态。
- 仅恢复仍在平台队列内的更新，不保证超过24小时的消息、不承诺零缺口或跨平台exactly-once。模拟8天是调整测试库时间，不是等待8天或完成24小时稳定性验收。
- 迁移15尚未发布/部署；验证临时14→15、新库及同结构重放。**不支持把旧内部候选的同号schema15直接当成新结构升级**，本轮使用新冻结快照，不执行生产迁移。

## 3. 测试环境与隔离

- Go1.27.4、GCC14.2.0、Python3.12.14；NoneBot2=2.5.0，Telegram=0.1.0b21，Feishu=3.1.0，larkify=0.2.0；React19.3.1、TypeScript7.0.2、Vite8.3.8。
- 平台/模型/生产存储环境变量在测试子进程清空；使用测试库、假HTTP transport、固定合成凭据/消息及本机临时IPC。Python合成测试禁止socket/DNS连接；实际SDK被执行，但未访问真实业务平台。Go用已有离线缓存，不安装或升级Go依赖。
- 完整检查在独立LF候选执行；不写当前预览产物、不替换运行后台、不新起服务或第三批浏览器。原先两批浏览器失败保留。
- 未访问真实账号/聊天、生产加密库/DPAPI凭据或真实模型/平台接口；未真实发送、启用计划、生产迁移、部署或重启。依赖准备不等于业务测试，**不能宣称整个过程无网络**。
- **前端依赖副作用**：子任务曾以bundled Node+pnpm执行 `pnpm -C apps/desktop check`，在tsc前意外下载/安装及迁移共享node_modules（输出30包）。junction仍指向原共享依赖；tracked依赖版本/锁未变，但产生31334字节的未跟踪本地锁（17:23:05 +08:00）。保留未清理、不纳入提交；未回滚、再次安装或操作服务。该次tsc不代替隔离候选。随后只调用已有Node/Prettier/npm及现成依赖，不再使用pnpm。

## 4. 合成工程结果（不等于人工验收）

| 证据组 | 已实际执行的覆盖 | 结果与局限 | 对应FT |
| --- | --- | --- | --- |
| NBT-01 | Python common/dispatcher、实际Telegram/Feishu SDK配合假HTTP | 12+15+12=39个顶层测试通过，含闲置0重寻与Telegram75个负向子场景；不接真实平台 | 058、060、067—069 |
| NBT-02 | Go桥接argv/env/cwd/边界/重复键/深度/Unicode/超时/退出；严格DTO及canonical ID/offset | 定向、全检及最终八包race通过；正常Go子进程测试夹具5秒以容纳race退出等待，hang仍1秒，生产超时不变；不是OS沙箱 | 061、069 |
| NBT-03 | 临时SQLCipher库、24小时租期、空页不续期、epoch/revision/ABA、回滚/取消/溢出、稳定消息及14→15 | 18个Telegram store顶层测试通过；100条合成消息、1000次stale CAS、跨epoch读/置顶/归档/无AI。**不是100条业务/模型评测集** | 060—064 |
| NBT-04 | API鉴权/工作区/Agent/严格JSON/同意/方法、关闭屏障、闲置/重启/重新核验 | 定向/全检/race通过；错方法405、未知资源404与human_only边界保持；新API回归经两次重启仍正确0重寻/去重。先取消Telegram再等飞书join，不承诺撤销已提交事务 | 057—059、062—063 |
| NBT-05 | 飞书默认/显式legacy、缺runtime不claim、历史回执恢复、lost/protocol/receipt/21001/429/重放 | 定向/全检/race通过，含5次重放不再发及21001相同绑定计划撤销；仅证明本地一次性账本，不代表真实权限/手机送达 | 066—068 |
| NBT-06 | 实际前端客户端/Panel/Inbox配合fetch、假时间、hooks替身 | 首跑16/20组exit1，四处失败修复后20/20组、259个拒绝断言通过；最终18个前端脚本均通过。不是DOM/真实React调度/浏览器/原生验收 | 061、064—065 |
| NBT-07 | 准备脚本默认/CheckOnly、失败拒绝、CPython平台、34精确版本/pip check；独立wheel/RECORD/声明核对 | 通过；先前CheckOnly前后5431个venv文件name/length/mtime不变，34个公开非撤回wheel/hash及2410个安装RECORD哈希核对。未执行准备脚本安装分支，仍待非作者审查 | 070 |
| NBT-08 | 三个固定Telegram入口、13键状态/10键结果、19位MaxInt64游标、人类专用、无发送/历史路由、飞书元数据 | 最终候选契约检查通过；API合成回归验证严格方法/JSON边界，不给模型开放入口 | 057、059、061、066 |
| NBT-09 | 完整Go/vet/契约/启动脚本/Python/前端/格式/类型/构建，八包race | 最终candidate-r5基于并行已提交的b9420cb，全检93.145秒、八包无缓存race86.324秒通过，均捕获native_exit_code=0；53项NoneBot功能文件与r3一致。r3通过、r4导出格式失败及其race通过均另保留，不以旧整树结果代替新基线复验 | 原FT与057—070 |
| NBT-10 | Bugbot与Security Review各一次AI辅助只读审查 | 两项已完成，**仅针对修复前候选**；Bugbot确认闲置P1，Security无新增已确认漏洞。P1已修复并自动化复测；没有再次调用两项审查，不声称修复后独立复审通过。AI不代签人员批准 | 审查门槛 |

## 5. 失败历史、冻结与证据

- 初始基线为 `ab0675da701db39136bfae0c10fd376567de0d6e`，独立LF基线全检通过。白名单从54扩至58项时只增加相关契约/重启/epoch/integration回归；无关本地锁始终排除。最终整树基线变化及复测另列，不以旧基线结果代替。
- candidate-r1完整门禁51.591秒exit1：Assistant能力数/人类专用断言、连接能力fixture、迁移marker被测试删除但表保留的重放失败。race同样失败且正常Go子进程夹具1秒与race退出flush冲突；没有把“无DATA RACE报告”记成race通过。另一次store定向编译因未使用import失败，修复后重跑通过。
- candidate-r2完整门禁83.395秒exit1：Go/vet/契约/启动脚本/39项Python/前端合成已通过，在三文件Prettier格式处停止；**类型检查/构建当次未运行**。只机械格式化三文件；已有Prettier的debug-check在非忽略路径验证格式安全，无新依赖。一次可选AST核对因不存在的TypeScript JS包失败，未安装修复。
- candidate-r2八包Go race均显示通过，但包装器错误使用 `true` 而非PowerShell的 `$true`，整体exit1且结果JSON未成功写入，不记为完整门禁通过。修正后candidate-r3全检89.978秒、race85.227秒均exit0；补齐原生stdout的同候选全检45.442秒（Go缓存）通过。
- 首次race补录因相对日志路径在Push-Location后解析错误，**测试尚未运行**（native_exit_code=-1、passed=false）；改为请求前解析的绝对日志路径，保留失败后75.638秒重新捕获通过。不把证据包装失败隐藏成未发生。
- 预提交范围门禁曾拒绝三份进度文档的并行T06变化，未覆盖或暂存。并行工作独立提交为 `b9420cbab45104280d49ab463a58a8bd3c44a5bd`，保留其为父提交及文档中的既有T06记录；重新冻结时，53项NoneBot功能/测试/契约/迁移/脚本的LF指纹与r3一致，五项进度/验收文档另行登记。
- candidate-r4在新基线的完整检查0.633秒exit1：Git归档把基线Go转为CRLF，停在格式门禁，**功能测试尚未执行**；抽样换行归一化后与父提交blob相同，未修改或格式化并行微信源码。该失败未生成独立stdout文件，保留transcript和JSON，不补造缺失输出。该候选八包无缓存race83.862秒通过，不将其代替完整门禁通过。
- 从同一b9420cb以命令级 `core.autocrlf=input`、`core.eol=lf` 重新导出candidate-r5，冻结前检查全部基线Go为LF。最终全检93.145秒、八包无缓存race86.324秒均通过并捕获原生stdout/native_exit_code=0；r4原始失败/成功记录不覆盖。r3与r5的53项NoneBot功能文件一致，不宣称两者整个基线树相同。
- 保留前端16/20首次失败、两批旧浏览器失败、此前SQLCipher C警告、Node实验性TS警告及Vite >500kB警告；构建成功不等于没有警告或性能已验收。
- 仓库留存：[结构化结果及原始摘要](<artifacts/2026-11-19-nonebot/validation-results.json>)、[完整补录stdout及失败包装记录](<artifacts/2026-11-19-nonebot/test-output.txt>)、[历史r3的58项LF指纹](<artifacts/2026-11-19-nonebot/candidate-r3-manifest.json>)、[最终r5的58项LF指纹](<artifacts/2026-11-19-nonebot/candidate-r5-manifest.json>)。原始本机日志不覆盖；发布副本移除transcript机器身份头、替换绝对本机路径并规范化LF/行末空白与ANSI显示控制；保留失败/警告及原始SHA256，不补造缺失的原生输出。bootstrap的“PowerShell未找到”TerminatingError为9个负向子例中的预期拒绝，不是随后成功门禁的隐藏失败。
- 最终冻结后只更新本页、FT清单、三处进度文档及上述四份证据；**53项功能、测试、契约、迁移及安装脚本仍与通过的candidate-r3和candidate-r5逐文件一致**。提交前按LF与62项白名单再次校验，排除无关本地锁；保留并行父提交，不把其六项微信源码纳入本轮新增提交。提交哈希生成前不编造，上传成功不等于验收完成。

复核核心命令（在独立候选、合成环境及已有依赖运行，不在生产库/现有预览产物目录执行）：

```powershell
./scripts/check.ps1 -IncludeNoneBot -NoneBotVenvDirectory '<已准备的隔离venv>'
go test -race -count=1 -p 2 -tags sqlite_fts5 ./internal/connector/nonebotbridge ./internal/connector/telegrambot ./internal/connector/feishu ./internal/store ./internal/api ./internal/config ./internal/connector/qqmilky ./tests/integration
```

## 6. 下一步及未通过门槛

1. joey审后端、angjustinl审前端；本人编写的测试/交付脚本由非作者技术成员审。AI报告和本轮修复后的自动化复测不能替代这些正式批准。
2. kirinbao另行授权并逐项组织FT-001—070；真实平台/模型/手机/原生、浏览器布局/键盘、100条业务评测及24小时稳定性仍未完成，不代用户启用或支付。
3. 微信自动回复/定时发送继续暂停；QQ仅Milky1.3；个人Telegram分支未合并；通用规则/审批/发件箱/SSE及完整持久摄取仍未整体完成。完成统计保持MVP0/27、扩展0/5，M4未通过。
4. 用户授权范围仅必要检查后的普通提交并推送当前 `feat/mcp-service`；不建PR、不更新main、不部署/重启或切换当前服务。
