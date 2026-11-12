# 自动获取聊天记录参考项目

核验日期：2026-11-12（Asia/Shanghai）。关联 FEAT-T06 / CHG-T06-002，影响 T02/T05/T07/E04 的后续接入设计。

目标是让用户连接账号、选择联系人后，由后台同步记录，减少导出文件、转换字段和上传文件的操作。本轮完成项目与源码核验，尚未实现本产品自动同步，也没有登录任何真实账号。

## 优先采用的路线

- **QQ历史：QQ Chat Exporter（QCE）HTTP API。** 已有历史查询、任务和定时导出实现；本产品可直接读取接口并自动规范化，不要求用户转换JSON。[项目](https://github.com/shuakami/qq-chat-exporter) · [源码与接口核验](qq-chat-exporter.md)。
- **QQ新消息：现有OneBot服务。** 延续已有账号核验和文本转换，后续实现事件监听及断线补查。QCE和OneBot是两种不同的服务接口；仅有OneBot地址不表示已有QCE。
- **Telegram个人账号：gotd/td。** Go MTProto客户端，支持用户账号授权、历史分页及更新机制；更贴近本人账号同步需求。[项目](https://github.com/gotd/td) · [采用条件](telegram-personal.md)。
- **微信：分开评估。** ChatLab不是微信采集器，Chatlog原仓已撤下；UI Automation和官方ClawBot的范围也不同。现阶段没有已验证、可直接内置的普通个人好友历史采集方案，不能承诺已解决。[微信核验](wechat-status.md)。

## 实施顺序

详见[接入步骤](接入步骤.md)：先QQ只读历史自动读取，再补OneBot新消息/在途缺口和持久化同步，之后做Telegram个人账号授权；微信按实际采集边界另验。自动获取记录不自动获得AI外传或发送权限。

已保留QCE固定提交的13份稀疏参考文件，见[本地参考源码](../源码/qq-chat-exporter/)（项目根路径：`参考项目/源码/qq-chat-exporter`）；源码仅保留本地，不进入本项目仓库。准确路径、提交及文件校验见[sources.json](sources.json)。没有安装依赖、执行上游程序或下载登录凭据。

## 核验时发现的上游变化

- NapCat当前main仅保留Ending说明；API文档和旧版本仍有历史能力证据，不能把缓存页面当作当前可直接构建的源码。[当前仓库](https://github.com/NapNeko/NapCatQQ)。适配以用户现有实例的能力核验为准。
- Chatlog作者已移除代码与历史并停止支持，本次不将旧镜像列为新部署依赖。[原仓说明](https://github.com/sjzar/chatlog)。
- 各参考项目的许可、发布版本和接口范围分别记录；没有把上游的自测或声明当作本机实测。

- 实际检查：QCE固定HEAD及13份文件SHA256一致，工作树干净；专题JSON与本地链接检查通过；PowerShell解析及源码包实际生成通过，166份包内文件逐一校验，6份专题资料入包、上游源码克隆及凭据被排除。只有资料/打包目录变更，本轮未重跑无变化的Go/前端功能测试，也未宣称真实同步通过。
