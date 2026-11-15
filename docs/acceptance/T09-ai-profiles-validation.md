# AI联系人档案与公开资料验证

- 开始：2026-11-14；集成检查日期：2026-11-15（Asia/Shanghai）。
- 关联：CHG-T05-004 / T09-002 / T10-002 / T11-002 / T13-002 / T14-002 / T15-001 / E01-001。
- 后端/存储/模型angjustinl负责、joey审；界面joey负责、angjustinl审；kirinbao组织验收。
- 状态：工程交付及集成检查通过，子项待指定人员审查/真实验收；整项进行中，实际完成日期—。

## 交付范围

加密v6档案/分类/候选/来源/作业，严格结构化模型协议，固定Tavily公网搜索，成功监听后启动的后台工作器，人工确认和Markdown导出，联系人目录和分类筛选。自动/联网默认关闭，候选不自动确认，不合并跨平台同名身份，不执行发送。

## 已知检查

模型/publicinfo/config专项测试与vet已通过：原Analyze协议保持，新增organize/enrich证据和输出验证；固定最小查询、TLS/公网DNS、重定向/凭据回显/响应上限及取消；配置脱敏。无真实模型/搜索外呼。

完整 `scripts/check.ps1` 通过：Go格式、全仓测试/vet、OpenAPI契约、9项PowerShell启动合成和6项目录权限/DPAPI启动夹具、新档案客户端/组件及既有连接/微信监控/导入回归、前端格式/类型/生产构建。构建JS359.29kB、CSS44.63kB；未验证原生Tauri打包。

存储9组档案顶层测试覆盖加密v6迁移/旧证据保留、身份及分类游标、手工覆盖、候选确认/来源、JSON消息预算、日额度、重开、两类作业跨Store串行、过期租约fence、暂停及1001个无变化联系人不阻塞新消息调度。新增额度上下文用例验证等待锁时取消即返回。

API/worker共11组顶层、3个子项合成测试覆盖鉴权/CORS/严格输入/CAS、构造及GET无外呼、Listen后显式启动、页面取消后作业继续、公开字段最小外发、空结果不调用模型、额度、暂停/Close与迟到结果拒绝落库、伪造跨会话消息/公开来源拒绝。API及Store档案专项race检查通过。

前端验证人工资料无模型可保存、模型目标确认、默认不开自动化、候选确认、来源URL、分类筛选、未知结果复核、轮询保留dirty输入、已暂停仍可编辑人工资料和页面关闭不取消作业；App/Inbox/ProfilePanel/CSS机械检查未报告问题。合成组件测试不作为真人页面验收。

辅助复核发现并修复：公开补充继承私密昵称/职业、较长公开简介确认后超出模型事实预算、JSON转义膨胀预算、额度预检等待锁时忽略取消、公开摘要截断标记、UI错误码及契约来源数量不一致。模型输入只选不超过800字节、序列化总计4KB、最多8条已确认事实；超长事实仍保留在档案，不截断改写原事实。

主要交付：`internal/store/profile*.go`、`migrations/0006_contact_profiles.sql`、`internal/agent/profile.go`、`internal/publicinfo`、`internal/api/profiles.go` / `profile_worker.go` / `profiles_test.go`、`apps/desktop/src/ProfilePanel.tsx` / `profile-api.ts` / `profile.css`、`scripts/test-profiles.mjs`、OpenAPI和配置/ADR文档。

## 真实边界

本轮未读取真人聊天或真实浏览器存储，未访问真实模型/搜索密钥，不发起收费模型/搜索请求。合成UI/hooks不代替浏览器/Tauri真人验收；专属Markdown不自动写入项目目录。

用户需按本机配置说明启用模型/搜索后，选择明确授权的会话验证分类、文档、公开候选来源/确认、暂停及重启恢复。检索匹配结果均待核实；有界消息/事实窗口不是全历史或完整履历。
