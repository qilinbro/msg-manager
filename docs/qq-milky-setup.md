# QQ Milky：从安装到连接

更新：2026-11-17；`T02 / FEAT-T02 / CHG-T02-007`。当前 QQ 个人号仅使用 Milky 1.3。

出现“尚未配置”表示本产品还没有协议端连接信息。普通 QQ 客户端本身不提供 Milky 接口，需要先安装一个支持 Milky 的独立程序。Windows 首次使用推荐 **LLBot Desktop 的 Milky 模式**。

## 1. 安装并登录

1. 打开 [LLBot Desktop 官方 v3.1.2 下载页](https://github.com/LuckyLilliaDev/LuckyLillia.Desktop/releases/tag/v3.1.2)，在 Assets 中下载 `lucky-lillia-desktop-win-x64.zip`。
2. 完整解压到固定目录，例如 `D:\LLBot`，运行 `lucky-lillia-desktop.exe`。不要在压缩包内直接运行；该版本不要求另装 .NET。
3. 按程序提示准备组件、启动 QQ，并自行扫码登录本人账号。登录、二维码和 QQ 密码都在 QQ / 协议端处理，不填写到本产品。
4. 保持 LLBot 和本产品后台运行。

[官方安装教程](https://luckylillia.com/guide/choice_install)。上游安装文档中的旧文件名可能不同，以固定发布页资产名为准。本项目不打包或自动运行协议端，不代替用户登录。

## 2. 在 LLBot 启用 Milky

打开 LLBot 左侧 **Bot 配置**，按下面设置并保存：

| 项目 | 填写内容 |
|---|---|
| Milky | 开启 |
| 监听地址 | `127.0.0.1`，仅本机访问 |
| HTTP 端口 | `3010`，或自己未占用的端口 |
| HTTP 路径前缀 | 留空，使用默认 `/api` 路由 |
| AccessToken | 自己设置一个非空随机令牌；例如使用密码管理器生成至少 32 位无空白 ASCII 字符 |
| WebHook | 留空；本产品主动连接事件流 |
| OneBot / Satori | 本产品不使用，可关闭 |

HTTP 与事件 WebSocket 共用 Milky 端口。[官方配置说明](https://luckylillia.com/guide/config)。仅在电脑本机使用，不开放公网端口。

三个地址不要混淆：`3010` 是 Milky，`3080` 是 LLBot 管理页面，`17890` 是本产品后台。`3000` 常用于 OneBot，不能直接照抄旧配置。

## 3. 回到工作台保存连接

打开工作台 → **设置 → QQ 个人号（Milky）**，在“连接设置”填写：

| 字段 | 示例 / 来源 |
|---|---|
| Milky 服务地址 | `http://127.0.0.1:3010`，不要加 `/api` 或 `/event` |
| 本人 QQ 号 | 刚刚在 LLBot 登录的账号，不能填好友 QQ 或昵称 |
| 协议端 AccessToken | 第 2 步设置的相同令牌，直接填值，不加 `Bearer ` |
| 每次历史同步条数 | 高级设置，默认 20，范围 1—30 |

依次执行：

1. 点 **测试连接**。会检查本人 QQ 和 Milky 1.3 版本；只测试，不保存、不启用接收。
2. 核对测试返回的账号，勾选保存确认，再点 **保存连接设置**。成功后令牌输入框清空，只显示“已配置”；配置保存在本产品加密数据库，立即生效，正常重启无需重复填写或设置环境变量。
3. 点 **核验账号与版本**，核验当前已保存连接。
4. 填写明确选定的好友 QQ，点 **同步一页历史**；需要持续收件时，在后台接收名单中选择最多 20 个好友并显式开启。

修改任一连接字段后需要重新测试和确认。重新读取设置保留尚未提交的编辑；版本冲突时先刷新、核对最新保存内容再测试。网络失败或超时后先读取状态，变更操作不会自动重试。

保存连接会停止当前接收并清除原接收授权，包括其他曾用账号的授权，防止切换 A→B→A 恢复旧权限；聊天记录和联系人不删除。保存后须重新核验并选择联系人。单纯重启保留既有接收名单，但需新一次账号核验才恢复接收。

## 4. 常见问题

| 提示 | 处理 |
|---|---|
| 连接失败 | 确认 LLBot 已运行且登录成功；核对 Milky 是否启用、端口是否为 3010 |
| 鉴权失败 | 两边填写同一个 Milky AccessToken；不是 QQ 密码、管理页面密码或本产品服务令牌 |
| 账号不一致 | 本产品填写的本人 QQ 必须与协议端当前登录账号相同 |
| 版本不匹配 | 使用真实支持 Milky 1.3 的实现，不能修改版本字符串冒充兼容 |
| 配置接口不存在 | 后台仍是旧程序；停止旧后台后运行 `scripts/start-inbox.ps1`，再刷新工作台 |
| 加密存储未启用 | 用 `scripts/start-inbox.ps1` 启动，保留原数据库与密钥文件 |
| 保存时输入令牌为空 | 同一地址和账号可保留已有令牌；首次配置、更换地址或账号必须重新填 |

**原 Lagrange 指引修正**：本轮核验的 Lagrange.Core `e1bd4baa` 仍报告 Milky **1.2**，本产品会拒绝；其 Releases 中的旧 OneBot 包也不是 Milky 安装包。新手按上述 LLBot 路线，不必再寻找 Lagrange OneBot。证据见 [Lagrange 版本常量](https://github.com/LagrangeDev/Lagrange.Core/blob/e1bd4baa16fa86ef000154e3eecb7816a29b63a7/Lagrange.Milky/Constants.cs)。

## 数据、授权与兼容方式

配置读取接口只返回地址、账号、条数、版本号和令牌是否存在，不返回令牌。令牌只保存在后台 SQLCipher，不进入浏览器持久存储、聊天模型、URL 或日志。用于连接本产品的服务令牌与 Milky 协议端令牌是两件不同的凭据。

已保存的连接优先于 `QQ_MILKY_URL`、`QQ_MILKY_ACCESS_TOKEN`、`QQ_MILKY_ACCOUNT_ID`、`QQ_MILKY_HISTORY_LIMIT` 环境设置；没有保存记录时才兼容这些启动配置。环境设置不会自动写入数据库，`.env.example` 也不自动读取。更换地址时不会沿用旧令牌。

协议端是外部独立进程，只通过本机 HTTP / WebSocket 通信。本产品不分发或链接其程序。LLBot 核心为 GPL-2.0，Lagrange.Core 为 GPL-3.0，各自许可和固定版本需随发布资料登记。[LLBot 核心源码](https://github.com/LuckyLilliaDev/LuckyLilliaBot/tree/de602971f850c3965a9b348e7d1f3e723eb1eba7)。

仅支持授权好友私聊文本；发送、群聊和媒体正文未开放。历史同步是最近一页，不是全量历史；断线可能有缺口。一次核验不代表持续在线，电脑休眠、关机或协议端/后台退出后不会继续接收。

## 接口与工程验收

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/v1/connections/qq-milky/config` | 脱敏读取保存配置，零协议端请求 |
| POST | `/api/v1/connections/qq-milky/config/test` | 只测试候选配置，不改变现有连接 |
| PATCH | `/api/v1/connections/qq-milky/config` | 显式确认、版本校验、加密保存及暂停原接收 |
| GET | `/api/v1/connections/qq-milky/status` | 缓存状态 |
| GET | `/api/v1/connections/qq-milky/verify` | 核验已保存连接，允许恢复原已授权名单 |
| POST | `/api/v1/connections/qq-milky/sync` | 一页好友私聊历史 |
| POST | `/api/v1/connections/qq-milky/monitor` | 显式接收名单和开关 |

全部接口遵守本地鉴权、来源、Host 与工作区校验；配置 JSON 限 16 KiB。协议端错误只返回固定分类。模型和 MCP 不获得配置/测试/令牌操作工具。

验证及待验收项见 [T02 配置流程验证](acceptance/T02-milky-setup-validation.md)。joey 后端正式审查、angjustinl 前端正式审查、kirinbao 本人登录与真实消息/浏览器/Tauri 验收待执行，实际完成日期为 —。
