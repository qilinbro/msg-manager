# QQ OneBot 接入说明（已退役）

用户于 2026-11-17 明确取消 QQ 个人号 OneBot 接入，统一使用 **Milky 1.3**（`FEAT-T02` / `CHG-T02-006`）。本文件只保留旧文档链接的迁移入口，不再提供 OneBot 启动步骤。

请使用 [QQ Milky 接入说明](qq-milky-setup.md)。旧 `QQ_ONEBOT_*` 环境变量、账号核验接口和设置页入口已取消；OneBot 地址或令牌不会自动转换为 Milky 配置。

迁移步骤：

1. 停止旧后台，从本机启动环境移除 `QQ_ONEBOT_URL`、`QQ_ONEBOT_ACCESS_TOKEN`、`QQ_ONEBOT_ACCOUNT_ID`。
2. 自行准备已登录本人账号的 Milky 1.3 协议端，在本机设置 `QQ_MILKY_URL`、`QQ_MILKY_ACCESS_TOKEN`、`QQ_MILKY_ACCOUNT_ID`。
3. 重启后台，在「设置 → QQ Milky」核验账号与协议版本；明确选择好友后再同步历史或开启后台接收。

此次移除不清理产品加密库中的既有聊天和联系人。发送仍关闭；Milky 真实接收和界面需独立验收。历史实现证据见 [OneBot 验证记录](acceptance/T02-onebot-validation.md)，本次检查见 [仅保留 Milky 的验证记录](acceptance/T02-milky-only-validation.md)。
