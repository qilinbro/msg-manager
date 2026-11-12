# 接入自己的 QQ OneBot 服务

本产品独立运行，不需要部署 AstrBot。参考 AstrBot 的 Bot 适配原则，对接你已有的 OneBot v11 服务。

## 准备

- 已由你本人登录 QQ 的 OneBot v11 协议端。
- 本机 **HTTP API 服务端地址**或 **正向 WebSocket 服务端地址**，对应的 OneBot Access Token 和预期登录 QQ 号。
- 地址仅支持 localhost、127.0.0.1、[::1] 的本机 HTTP/WS。远程服务、反向 WS 服务端监听和公网接入尚未实现。

如果协议端目前只有“WebSocket 客户端/反向 WS”配置，需要另开 HTTP 服务端或正向 WS 服务端；不能把 AstrBot 默认的 6199/ws 反向监听地址当作已有协议端地址。

## 在后台配置

打开 PowerShell 7，进入项目根目录。地址和账号在本机填写；下面的号是示例，须替换为你自己的预期账号：

```powershell
$env:QQ_ONEBOT_URL = 'http://127.0.0.1:3000'
$env:QQ_ONEBOT_ACCOUNT_ID = '123456789'
$qqSecret = Read-Host '输入 OneBot Access Token' -AsSecureString
$qqPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($qqSecret)
try {
    $env:QQ_ONEBOT_ACCESS_TOKEN = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($qqPointer)
} finally {
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($qqPointer)
    Remove-Variable qqSecret, qqPointer
}
$env:SOCIALD_API_TOKEN = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$env:SOCIALD_API_TOKEN | Set-Clipboard
.\scripts\dev-backend.ps1
```

OneBot Token、NapCat WebUI 密码、本产品 API 令牌是三种不同配置。不要在聊天里发送令牌。`.env.example` 仅说明变量，不自动加载；启动终端中的环境变量才生效。

## 在工作台核验

1. 按根 README 启动前端，在设置页粘贴本产品 API 令牌，连接 Go 服务。
2. 点击“核验 QQ 账号”。本产品只向后台已配置的 OneBot 调用 get_login_info、get_status。
3. 只有账号与 QQ_ONEBOT_ACCOUNT_ID 完全一致且在线检查通过，才显示“账号已核验”。
4. 检测是一次性读取，结束后关闭连接；不是已开启持续收件。核验通过不打开自动发送，也不产生测试消息。

API：`GET /api/v1/connections/qq-onebot/verify`，使用本产品 Bearer 鉴权。不接收调用者提供的 OneBot URL、令牌或账号。错误只返回固定分类，不回传协议端原始内容。

## 当前支持与后续

- 当前：HTTP/正向 WS 身份核验、账号不符拒绝、文本事件转换与隔离验证。
- 当前未启用：持续接收、消息入库、历史导入、群聊/多媒体、模型回复、自动发送。
- 下一步：你提供服务地址与实际连接模式后进行真实只读核验；T03/T05/T06 完成持久化后接入收件，T16/T17 完成审批与发件箱后再开放授权发送。

连接失败可按工作台分类检查未配置、不可达、令牌错误、QQ 未登录、账号不符或协议错误；不能仅凭 HTTP 200 就判定 QQ 可用。
