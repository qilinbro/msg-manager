# 统一消息中心使用说明

当前可以统一管理 QQ、微信、Telegram、闪聊的**已导入文本记录**：按平台搜索会话、查看历史、标记已读/未读、置顶、归档和恢复。不同平台、不同本人账号及不同联系人 ID 分开保存，同名不会自动合并。

## 启动

Windows、PowerShell 7.2+、Go 1.26+、Node.js 24 和 GCC 为当前开发环境要求。在项目根目录运行：

```powershell
.\scripts\start-inbox.ps1
```

首次启动会生成数据库密钥，以当前 Windows 用户的 DPAPI 加密保存在 `data/private/inbox-key.dpapi`；数据库位于 `data/messages.db`。脚本把本产品访问令牌复制到剪贴板，不显示数据库密钥。之后启动会使用已有密钥，不会重新加密覆盖已有记录。数据库和密钥文件均被排除上传。

另开终端启动前端：

```powershell
cd apps/desktop
npm ci
npm run dev
```

打开 `http://127.0.0.1:1420`，在设置中粘贴访问令牌并连接，再进入“消息”。

密钥文件只能由同一 Windows 用户解密；不要删除或移走它。仅复制数据库到另一台电脑不能恢复记录，跨设备加密备份与恢复尚待 T24 完成。脚本适用于当前开发阶段，后台服务安装尚未实现。

也可通过 `.env.example` 中的 `SOCIALD_DATABASE_PATH` 与 `SOCIALD_DATABASE_KEY` 在终端手动配置：本机固定磁盘绝对路径、32 字节随机密钥的 64 位十六进制字符串，两个变量必须一起设置；数据库路径拒绝链接、junction 和网络盘。密钥与本产品 API 令牌分开保管。

## 导入与管理

1. 点击“导入消息”，选择平台与本人账号，并选择 JSON 文件。
2. 点击“检查文件与来源”，核对平台、本人账号与条数，再确认导入。
3. 在“全部平台”中统一查看，也可以按平台、未读、置顶或归档筛选。
4. 搜索联系人、账号、对方 ID 或消息正文。选择会话查看历史，使用“标为已读”“置顶”“归档”等管理按钮。
5. 管理操作只改变本地状态，不改变原平台未读状态，也不向联系人发送消息。打开会话本身不会自动标已读。

每次最多 8 MiB、1000 条记录；正文最多 16000 UTF-8 字节；标识和名称最多 128 个字符。正文时间必须是包含时区的 RFC3339 时间。支持中文短词的字面子串搜索，目前不使用 FTS5 做搜索排序，FTS5 建表和索引触发器已验证。

同一平台、本人账号、对方 ID 和原消息 ID 相同且内容一致时计为重复；相同 ID 内容不同则整批回滚，不覆盖原记录。导入失败不会部分保存。分页绑定当前数据版本，导入或管理后会刷新列表；旧游标失效时重新读取第一页。

## 四平台通用 JSON

可以上传下面完整对象，也可只上传 `messages` 数组，并在界面填写平台和本人账号。以下均为**合成格式示例**，不会自动进入产品。

```json
{
  "platform": "qq",
  "account_id": "synthetic-self",
  "account_name": "合成格式示例",
  "messages": [
    {
      "external_id": "source-message-1",
      "peer_id": "synthetic-peer",
      "peer_name": "合成联系人",
      "direction": "incoming",
      "body": "这是一条格式示例。",
      "occurred_at": "2026-11-12T08:00:00+08:00"
    }
  ]
}
```

`platform` 只能取 `qq`、`weixin`、`telegram`、`shanliao`；`direction` 只能取 `incoming`、`outgoing`。标识必须来自用户已有记录，不以昵称猜测身份。QQ、微信、闪聊的不同导出工具格式尚未逐个适配，需先转换为上述格式。

## Telegram Desktop 导出

支持 Telegram Desktop 导出的**单个个人私聊 JSON**（`type: personal_chat`）。在界面选择 Telegram Desktop 格式，填写本人数字账号 ID，不能用用户名代替；来源、方向以导出中的稳定 ID 与显式填写的本人 ID 校验。

字符串和可见文字片段会转换为纯文本；服务消息、附件、多媒体和无法确认发送者的记录跳过，并在结果中显示数量。不会读取附件路径或提取实体中的隐藏身份。全账号导出、群聊暂不支持，不能用导入声明实时连接。

[Telegram 官方导出说明](https://telegram.org/blog/export-and-more)；转换依据见 [官方 JSON 序列化源码](https://github.com/telegramdesktop/tdesktop/blob/dev/Telegram/SourceFiles/export/output/export_output_json.cpp)。

## 在线接入状态

- QQ：已有 OneBot 只读身份核验；持续收件尚待真实服务地址与 T06，核验不表示消息已连接。
- 微信、Telegram：本轮管理导入记录，未开启实时接收。
- 闪聊：具体产品/接口尚待确认，仅提供统一格式导入。
- 发送、模型回复、审批、平台历史补齐均未启用。

本轮工程和界面验证使用合成数据。团队实名代码审查、真实账号授权与独立验收仍需完成。
