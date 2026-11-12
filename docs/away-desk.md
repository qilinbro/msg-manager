# 接班 · 比赛演示与复核

## 网页原型

在 `apps/desktop` 安装依赖并运行 `npm run dev`，打开 `http://127.0.0.1:1420/?demo=away`。首屏给出优先事项、截止时间和回信草稿。点击「一键演示」，用 7 秒回放离席 30 分钟的合成场景，依次展示离开、收信、提取交付要求和准备回信，随后自动回到交接台。也可点击「跳到结果」。

投屏时点击大号截止时间，直接定位原邮件证据。点「审阅回信」可编辑、保存和复制。「离席授权」保留合成场景选项，可从「运行记录」投递示例邮件检查异常与恢复。「制作推广 Brief」可预览确认后的公开素材。

视觉以 [Finfold 创作台](https://www.finfold.app/workbench) 的浅蓝灰画布、白色圆角面板和深色主按钮为基准。待办的优先级与时间参考 [Flowkit](https://www.behance.net/gallery/244444063/Flowkit-Task-Management-Dashboard-Case-Study)，任务标题与留白参考 [Orriso](https://www.behance.net/gallery/240866253/Orriso-AI-Workspace-UX-UI-Dashboard-Design)。

## 90 秒实机演示

1. 按 [邮箱](email-setup.md) 和 [模型](ai-setup.md) 说明配置授权测试邮箱、模型和加密库，启动独立 Go 服务。使用专用测试邮箱与虚构的合作邮件。机器保持唤醒。演示前先验证只读同步能拿到 `UIDVALIDITY` 和 `UIDNEXT`。
2. 正常打开工作台，配对本机服务，选择「接班 → 离席授权」。填入另一个授权测试邮箱的发件地址，确认范围，点击「开始接班」并关闭窗口。
3. 从测试发件箱投递虚构邀请。工作台打开时，交接台会自动显示后台整理的结果。示例正文用清楚的日期、24 小时制时间和时区，如 `Please send the public description and a 60-second demo video by 18:00 Beijing time today.` 后台每 6 秒检查一次，等待时长取决于 IMAP 与模型。另保留一条不同发件人的控制邮件用于核对范围。
4. 重开工作台。当前配对令牌只在窗口内存中保存，按原流程在「设置」重新配对后进入「接班」。核对截止时间、原文高亮、任务和英文草稿，再展示运行记录及停止操作。连续成功三次后，录制一份标明「实机预录」的完整录像。

离席状态、邮箱收件位置、任务、草稿和记录存于加密库。只提交匹配发件地址的纯文本主题与正文给已授权模型。发件地址用于筛选。修改邮箱范围或模型配置后需要重新授权。重启后恢复未结束的会话和待处理项，中断时已提交模型的任务保留为待手写草稿，避免重复计费。格式不明确的日期、时区或时间会留待确认。邮箱轮询沿用最近一批的读取上限，遇到跳过或可能缺口会记录提示。

### 自动验收三轮

使用专用 QQ 测试邮箱。登录 QQ 邮箱网页版，进入「设置 → 账号与安全 → 安全设置」，开启 IMAP/SMTP 并生成授权码。按[邮箱配置说明](email-setup.md#本机配置)使用 `imap.qq.com:993` 启动后台，保持 Go 服务和前端运行。测试脚本通过 `smtp.qq.com:465` 向同一个邮箱投递虚构来信。企业邮箱使用管理员给出的 IMAP TLS 与 SMTP TLS 参数。[QQ 邮箱授权码说明](https://help.mail.qq.com/detail/106/985)

在项目根目录运行以下命令，将示例地址换成后台配置的专用测试邮箱。

```sh
python3 -m pip install playwright
python3 -m playwright install chromium
python3 scripts/verify-away-live.py --mailbox your-test-address@qq.com --smtp-host smtp.qq.com
```

按提示隐藏输入本机服务令牌和 QQ 邮箱授权码。脚本从正常工作台开始接班，关闭 Chromium 后向同一邮箱投递一封虚构邀请。后台备稿后重开 Chromium，核对截止时间高亮和草稿。脚本每轮显示耗时和结果，任一轮失败即停止。验收使用浏览器工作台，Tauri 原生桌面运行仍需实机验证。

本机 SMTP 无法连接时，命令改用 `--manual-delivery` 代替 `--smtp-host smtp.qq.com`。每轮在 Chromium 关闭后，从 QQ 邮箱网页版按终端显示的主题和正文发给自己，再按回车。后台仍通过真实邮箱收件，余下步骤由脚本检查。

## Finfold 赛后接入

默认关闭。先在 Finfold 测试环境应用迁移 131，启用 `FINFOLD_AWAY_HANDOFF_ENABLED=true`，在两端后台配置同一份仅服务端使用的试点凭据 `FINFOLD_AWAY_HANDOFF_SECRET`，在 sociald 设置 `FINFOLD_AWAY_ORIGIN=https://staging.finfold.app`。测试确认后可为生产另行配置对应域名和独立凭据。凭据留在服务端环境。

用户确认四个公开字段后，桌面后台发送它们并打开系统浏览器。导入凭证有效期为 15 分钟。登录后在当前 Finfold 账户建立独立素材草稿，重复兑换返回同一草稿。邮箱正文、联系人、附件和回信草稿均留在本地。

## 验证状态

- 本机 Chromium 在 1280 × 720 和 390 像素宽度走查通过，覆盖原文、键盘焦点、草稿、Brief 确认、关窗再开和三类异常。
- 使用合成邮箱和本机 Qwen2.5-3B-Instruct 连续完成三轮 Chromium 关窗、后台收信、建任务、备稿和重开复核。各轮都核对了 18:00、北京时间、两项要求、原文高亮及英文草稿。另验只读 IMAP TLS 的新邮件检查、加密库重启恢复、截止时间判定和停止后迟到结果。
- Finfold 本地 API 测试及隔离 PostgreSQL 验证了凭证字段、账号隔离、幂等、权限和到期。
- 仍需在真实授权邮箱连续完成三轮、Windows 原生运行、3 位首次使用者计时、Finfold 测试站端到端及非作者审查。比赛录像须从真实验收运行录制。
