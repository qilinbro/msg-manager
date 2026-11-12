# 运行边界

sociald 是独立进程，桌面窗口通过本机鉴权 HTTP API 访问它。当前提供开发运行入口，不随 Tauri 启停后台。

- Windows 服务安装、自动恢复与开机启动属于 T19，当前没有实现。
- SQLite/SQLCipher 初始化属于 T03/T05，当前 readiness 返回 503，不能按生产可用服务交付。
- 微信 Clawbot 与 Telegram 注册项仅描述计划能力；尚无真实账号连接或发送。
- 部署时保留 loopback 监听、随机配对令牌和非作者审查；本版不提供远程公网模式。
