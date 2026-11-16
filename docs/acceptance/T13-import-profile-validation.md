# 模型配置、全历史整理和档案关联工程验证

日期：2026-11-16（Asia/Shanghai）。关联FEAT-T05/T07/T09/T10/T11/T13/T14/T15/E02；稳定变更号分别为CHG-T05-005、T07-002、T09-003、T10-003、T11-003、T13-003、T14-003、T15-002、E02-001。工程实现已交付，正式审查与真实验收待进行；实际完成日期—。

## 交付

- `migrations/0008_ai_model_config.sql`、`0009_profile_links.sql`：模型设置和档案关联进入产品SQLCipher加密库；现有数据迁移不删除聊天。
- `internal/store/model_config.go`、`internal/api/model_config.go`：版本化读取/保存、密钥只写、目标校验、运行中一致配置切换及重开恢复；`AIModelPanel.tsx`为实际保存表单。
- `internal/store/import.go`、`internal/api/inbox.go`、`Inbox.tsx`：默认关闭的导入AI授权，绑定目标模型，消息/授权/作业原子保存。
- `internal/store/profile_jobs.go`、`profile_results.go`、`internal/api/profile_worker.go`、`internal/agent/profile.go`：自动按序处理完整已入库文本，使用昵称/平台ID/人工备注和当前批证据；承接上批摘要。个人档案公开进度条数。
- `internal/store/profile_links.go`、`internal/api/profile_links.go`、`ProfileLinksPanel.tsx`：身份建议、明确确认、解除及按来源分隔的联合文档，保留原会话权限。

## 实际工程检查

全仓`scripts/check.ps1`通过：Go格式、`go test -tags sqlite_fts5 ./...`、Go vet、OpenAPI契约、PowerShell启动9个子场景与选择器/参数检查、启动器6个合成场景、全部既有前端回归、模型配置和档案关联实际客户端/组件测试、Prettier、TypeScript及Vite生产构建。产物JS386.85kB、CSS45.10kB仅为该次构建记录。

新增`scripts/test-import-analysis.mjs`另行执行通过并已纳入检查入口：默认不勾选、展示目标及昵称/ID范围、一次带模型授权的导入请求、成功显示排队数量、409不自动重试、模型变化重新确认、旧服务无排队字段不能假报已排队。UI机械检查输出空问题列表。

专项检查使用`go test -race -tags sqlite_fts5`运行Store/API对应测试，覆盖：

- 模型设置：加密重开恢复、GET不返回密钥、格式化对象不输出密钥、修改地址必须重填密钥、额度0和大于1000拒绝、配置版本冲突、保存成功清空输入/失败保留及组件卸载。
- 全历史：100条合成消息分5批并重开恢复、每条均纳入顺序水位；伪造批尾不跳过消息、手动近期整理不推进自动水位；重复导入不重跑、暂停导入回滚、超长JSON明确失败；API用合成模型执行25条两批真实编排。
- 数据限域：当前会话证据、昵称和ID；公开补充不继承私聊/私人备注/账号ID/历史摘要；候选未核实，原人工资料和确认事实保留。
- 关联：同名、公开姓名和同平台账号仅为建议；跨平台同ID不作为依据；双向幂等、最多20条直接关联、解除后原数据保留；联合导出不扩大AI授权。

所有模型、协议、账号及消息为合成数据，SQLCipher临时库不含真实资料。未读取用户实际配置、密钥或聊天，未调用真实模型/Tavily，未发送任何平台消息。实际浏览器视觉验收、Rust/Tauri原生构建与打包未执行。

## 人员与待验收项

后端/AI和存储负责人angjustinl，指定审查joey；前端/UI负责人joey，指定审查angjustinl；kirinbao组织真实验收。开发代理进行辅助交叉检查，不作为上述人员批准；实际审查人、批准日期及最终完成日期待定。

- [ ] joey审后端：模型切换与授权、加密保存、导入事务、批次租约、候选证据及关联范围。
- [ ] angjustinl审前端：密钥输入生命周期、确认内容、进度准确、未知结果及关联撤销。
- [ ] kirinbao以本人授权的小批文本保存真实模型，导入后核对已处理/总条数，重开继续且无遗漏、同名不同ID独立。
- [ ] 核对事实引用和跨批摘要，确认候选不自动升为事实；修改模型目标后旧授权不恢复，需重新确认。
- [ ] 同一人的两个会话确认关联、导出并解除；比对消息数量、证据及AI权限未改变。

保留T01—T27/E01—E05原计划与整项指标，完成统计0/27、0/5。本轮只交付上述可独立验证子项，未将原依赖M4或全产品整体验收视为通过。
