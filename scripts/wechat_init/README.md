# 本机微信密钥初始化 worker

这是产品入口调用的一次性本机组件，不是后台监听器。真实运行必须由用户主动发起并确认自己的账号；开发检查不会自动调用 Frida、微信进程或真实聊天库。

## 输入契约

- `--pid`：唯一允许连接的现有微信进程 ID。
- `--created-filetime`：该进程创建时间的 Windows FILETIME 整数，防止重用错误 PID。
- `--dll`：该进程已经加载的 `Weixin.dll` 显式绝对路径。
- `--database`：所选账号目录下唯一的 `db_storage/session/session.db`。
- `--account-id` 与 `--consent-account`：必须精确相同；由外层用户确认流程提供。
- `--output`：已经设置受保护 ACL 的私有目录中的新文件；不覆盖文件，不接受链接。
- `--timeout`：总期限秒数，默认180，最大300。

仅接受 Windows、本机固定磁盘、当前 Windows 用户拥有的 `Weixin.exe`，以及严格匹配的微信/DLL版本 `4.1.15.56`。持有已验证进程句柄直到结束；不枚举其他进程或账号，不自动提权、启动或关闭微信，不允许手动指定挂钩地址。

## 状态与结果

stdout 仅输出逐行 JSON：`event` 为 `ready`、`waiting`、`saved` 或 `error`；最后两者包含安全的 `code`。外层入口应在收到 `ready` 后才提示用户自行退出登录并重新登录。进程退出后不追踪新 PID，失败后需重新发起。

Frida 脚本仅在限定进程内等待密钥设置调用，最多12次调用、64KiB材料；Python侧重复校验上限，只验证256个以内的32字节候选。材料不写文件、不输出。候选仅使用指定数据库第一页的 HMAC 校验。Python与第三方运行时可能暂存不可变字节，程序不承诺无法被本机调试器读取的内存擦除；进程为一次性运行，清理钩子、断开会话并退出。

成功文件字段为 `format:1`、`account_id`、`client_version`、`protected_key`。后者是当前用户 DPAPI 密文的 base64，额外熵固定为 UTF-8 `msg-manager/wechat-init/v1`。stdout不含密钥；账号、路径和原始异常不进入状态流。外层入口须验证账号及数据库可读性，再重新封装到产品正式配置，不能仅凭输出文件存在就宣布成功。

Frida 是可选初始化依赖，当前限定隔离虚拟环境中的 `17.26.3`，导入时排除项目目录，拒绝同名本地文件及其他版本；正常收件不需要它。离线第三方代码范围、修改和 MIT 归属见 `third_party/flandora_wxkey/SOURCE.md` 与 `LICENSE`。

重新登录可能正常更新数据库第一页。工具绑定原文件身份与盐值，重新检查路径后验证最新页的 HMAC，允许同文件的正常提交；换文件、换盐值或最新页验证失败均不保存结果。

## 开发验证

运行 `python -m unittest scripts.wechat_init.test_worker -v`。Frida/微信目标均为 mock；唯一真实进程元数据测试只读取测试 Python 自己的明确 PID。第一页使用仓库现有 SQLCipher4.4.2生成的公开合成加密 fixture，附生成器；没有真实账号数据。

以上检查不代表真实微信挂钩已经成功，不代表指定成员已审查，也不代表业务验收完成。
