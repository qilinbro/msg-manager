# 微信自动获取记录的核验结果

日期：2026-11-12。以下均是官方仓库/文档核验，没有读取本机微信数据，也没有部署采集器。

## ChatLab：参考同步协议，不是采集源

[ChatLab/ChatLab](https://github.com/ChatLab/ChatLab)，固定main提交`c258a139d323e0743ff06d82d915a726d1c2a733`，[AGPL-3.0](https://github.com/ChatLab/ChatLab/blob/c258a139d323e0743ff06d82d915a726d1c2a733/LICENSE)。

官方[导出指南](https://github.com/ChatLab/ChatLab/blob/c258a139d323e0743ff06d82d915a726d1c2a733/docs/cn/usage/how-to-export.md)说明其消费已导出数据，不提供抓取。已有[Pull协议](https://github.com/ChatLab/ChatLab/blob/c258a139d323e0743ff06d82d915a726d1c2a733/docs/cn/standard/chatlab-pull.md)用于发现会话、历史及增量拉取，但前提是另有可用数据源。适合参考“连接一次、选择会话、后台同步”的交互和游标边界，不能据此宣称微信已自动接入。

## Chatlog：原仓已停止提供代码

[sjzar/chatlog](https://github.com/sjzar/chatlog)，核验HEAD`7dad93d7b55a1f801bad3be16620083d2f998599`。作者2025-10-20公告撤下全部代码和历史并停止支持；当前主仓仅有[移除通知](https://github.com/sjzar/chatlog/blob/7dad93d7b55a1f801bad3be16620083d2f998599/README.md)。本轮不寻找旧镜像恢复部署，不把搜索缓存中的旧API说明当成当前维护能力。

## wxauto4 / wxautox4：本机界面采集待许可与兼容验证

上游[文档](https://docs.wxauto.org/docs/install.html)描述用Windows UI Automation操作已登录客户端，接口含当前窗口、新消息监听和滚动历史读取；已有开发中的[HTTP桥项目](https://github.com/cluic/wxauto-restful-api)。它受界面可见范围、客户端版本和窗口状态影响。

当前Plus[发布者许可条款](https://docs.wxauto.org/legal/commercial-license.html)对作为第三方产品组件分发另有授权条件；旧仓LICENSE不能替代新包条款。当前不内置、不下载安装，不以界面采集代表完整历史。需要先确认适用许可和本机版本，再在用户已授权的可见会话范围验证；不采用内存取密钥、注入或绕过鉴权路径。

## 腾讯官方ClawBot：独立通道

[Tencent/openclaw-weixin](https://github.com/Tencent/openclaw-weixin)，固定main提交`0ad1ae91347a938f52f1e4e2abd6dcda51e05f9e`，MIT。官方[HTTP协议](https://github.com/Tencent/openclaw-weixin/blob/0ad1ae91347a938f52f1e4e2abd6dcda51e05f9e/docs/protocol_zh_CN.md)可供自有后台参考适配扫码绑定、长轮询与消息通道；不要求部署OpenClaw替代自有产品。

核验范围未发现普通个人好友/群历史枚举接口。该通道可以单独评估，不能当作接管全部既有微信聊天。

结论：**目前没有已核验、可直接内置并解决全部个人微信历史同步的方案。优先落地QQ，微信采集独立立项验证，保留导入兜底。**
