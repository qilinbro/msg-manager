# T04 本地交付与待办

- 日期：2026-11-12（Asia/Shanghai）
- 变更：CHG-T04-002；任务仍为进行中，实际完成日期为空。

## 当前交付

源码、OpenAPI、五页工作台、Tauri 外壳、图标、启动说明、验证记录与任务日志均已保存在项目目录。

使用 **PowerShell 7.2+** 执行 `pwsh -File scripts/package-handoff.ps1`，可在 `.runtime/handoff/` 生成带 SHA256 清单的源码 ZIP。包内保留项目代码、任务文档与参考项目索引；排除工具、缓存、构建输出、真实环境变量、数据库/WAL、私钥、证书、数据目录和上游源码克隆。当前项目没有真实聊天数据；后续源文件或样例如加入业务数据，仍须先审阅脱敏再打包。输出路径及既有项目内祖先拒绝链接或 junction。ZIP 内的 `HANDOFF-MANIFEST.json` 可逐文件核对大小和哈希，旁边的 `.sha256` 核对整个包。

## 最新同步进展（CHG-T04-003）

用户于 2026-11-12 再次明确授权上传。已重新读取远端 main，基线为 ff10ea483ba190e6448b368892b6938ab188f2c3，完整项目检查重新通过。独立分支 [feat/t02-qq-adapter](https://github.com/qilinbro/msg-manager/tree/feat/t02-qq-adapter) 已上传 T04 工程、T02 QQ 适配、最新文档和脱敏证据；通过 ls-remote 核对远端为 [35ecadef2e52b6cbf4471d88338d8f600cf75f4f](https://github.com/qilinbro/msg-manager/commit/35ecadef2e52b6cbf4471d88338d8f600cf75f4f)。未合并 main，也没有创建 PR。

本轮使用本机 Git 登录信息上传。首次在受限运行环境内读取凭据失败，正常提权审批后推送成功。没有读取或展示凭据内容，没有关闭 TLS 校验或强制更新远端。上传暂存工作树位于 `.runtime/github-upload-20261112/`，不进入上传范围。

本轮上传不使用旧 T04 ZIP 的任务标签作为 T02 的来源记录；上传快照同时关联 FEAT-T04 / FEAT-T02。工具、缓存、运行时、真实配置和上游源码继续排除。

## 前轮 GitHub 状态（保留历史）

前轮通过 GitHub API 创建了候选树和二进制对象，但在创建分支 `feat/t04-architecture-scaffold` 时，自动审批检查因工作区额度不足未能完成，工具明确表示未执行该动作。这不是操作被判定不安全，也不是已成功提交。

因此没有可报告的分支提交或草稿 PR 链接，main 中也不能视为已包含本轮代码。当前 GitHub 连接工具亦不可用；本轮不通过其他通道绕过该审批检查。恢复工作区额度及连接后，应重新读取远端最新状态，再以独立分支提交并创建草稿 PR。

## 仍需完成

1. 安装或使用可用的 Rust/Cargo、MSVC 和 Windows SDK，验证原生 Tauri 编译并生成 Cargo.lock。
2. joey 审后端，angjustinl审前端与桌面；kirinbao组织整体验收。
3. 已上传独立分支；指定成员正式审查后再决定合并，上传不代表开发任务已验收。
4. T01 继续确认范围、赛期、账号与预算，再按依赖开展数据库和平台真实接入。

事件流当前只是鉴权后的显式 501 预留接口，不具备实时订阅或恢复游标；这些能力应在持久化底座完成后另行实现。
