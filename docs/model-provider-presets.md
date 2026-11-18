# 模型预设与 Agent API 范围

关联 FEAT-T04 / CHG-T04-007、FEAT-T08 / CHG-T08-009、FEAT-T11 / CHG-T11-005。2026-11-18 实现；待指定非作者审查与真实账号、模型及原生客户端验收。本文不表示真实供应商已接通。

## 1. 配置流程

1. 连接本机服务，在设置的「AI 模型配置」选择预设，或手动填写地址。
2. 在供应商自己的控制台核对当前可用的**模型 ID**、原生工具支持及计费；本产品不附带默认模型 ID，不枚举账户模型。
3. 填写密钥及共享每日额度，核对目标后明确勾选并保存。云端 HTTPS 必须提供密钥；本机数字回环 HTTP 的密钥可选。
4. 保存不自动分析、探测、运行工具、启用计划或发送消息。Assistant 每轮另行同意，模型返回的写操作仍需逐项核对服务器固定参数并确认。

选择预设只填地址，清空当前表单里的模型 ID、临时密钥和保存勾选，不改后台已保存配置。手工修改地址也清除这些跨目标信息；修改模型 ID、临时密钥或每日额度会撤回保存勾选。单独重新读取预设不覆盖手工输入。预设读取失败不阻止手动配置。

已保存密钥仅在 **base_url 逐字相同的服务地址**下可留空保留；更换地址绝不继承旧密钥，公网 HTTPS 须重新提供该服务密钥。切换到通过完整校验的数字回环 HTTP 免密服务时，留空会清除旧云端密钥，不把它转发给本机服务。密钥加密保存、写入后不返回页面。保存沿用 `expected_revision` 并发比较；陈旧版本不覆盖新配置，失败保留输入。更换配置会撤销绑定旧模型 ID/版本的自动整理与计划同意。

## 2. 六项静态预设

| 预设 | 填入的 Base URL | 标记 | 说明 |
|---|---|---|---|
| OpenAI | `https://api.openai.com/v1` | `protocol_supported` | Chat Completions；不是 Responses |
| DeepSeek | `https://api.deepseek.com` | `protocol_supported` | Chat Completions；模型 ID 人工核对 |
| Gemini 兼容 | `https://generativelanguage.googleapis.com/v1beta/openai` | `protocol_supported` | 仅 OpenAI 兼容模式，不是原生 Gemini |
| DashScope 兼容 | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `protocol_supported` | 仅该官方兼容路径，不代填 thinking/模型参数 |
| 自定义 HTTPS | 空，由本人填写 | `template_only` | 不表示任意反向代理路径都可用 |
| 本机回环 HTTP | `http://127.0.0.1:11434/v1` | `template_only` | 仅地址模板；不表示某个本地引擎或模型已验证 |

`protocol_supported` 表示已实现相应线协议/路径并通过合成测试，**不等于真实连接、账户权限、供应商兼容性、模型 tools 能力或服务可用性已验证**。`template_only` 更不构成引擎兼容承诺。前端也支持禁用展示 `unsupported` 元数据，但当前注册表六项均不以此标记。

通用 Base URL 仍仅接受根路径或 `/v1`（可有一个尾斜杠）。本轮只额外允许：

- HTTPS、默认端口或明确 `443`，主机严格为 `generativelanguage.googleapis.com`，路径 `/v1beta/openai[/]`；
- HTTPS、默认端口或明确 `443`，主机严格为 `dashscope.aliyuncs.com`，路径 `/compatible-mode/v1[/]`。

最终追加 `/chat/completions`。其他主机套用上述路径、非 443 端口、路径变体/编码、查询/片段、用户信息、尾点别名、空端口冒号或 `0443` 不会获得新增兼容路径许可。原有公网 HTTPS、DNS 全答案过滤、固定目标拨号、证书/SNI、禁代理及禁重定向不变；HTTP 仅数字 `127.0.0.1` / `[::1]` 且必须明确合法端口。未实现 Anthropic 原生 Messages、OpenAI Responses、原生 Gemini 或任意 URL 代理。

## 3. 静态元数据接口

鉴权 `GET /api/v1/ai/model-presets`，响应 `Cache-Control: no-store`；不接受任何查询串，裸 `?` 也返回 400。其他方法不提供配置写入能力。无请求体、不传密钥、不访问供应商、不检测本地端口；设备令牌/服务令牌沿用现有受保护本机认证。

```json
{
  "protocol": "openai_chat_completions",
  "model_id_required": true,
  "live_verification_performed": false,
  "items": [
    {
      "id": "openai",
      "display_name": "OpenAI",
      "base_url": "https://api.openai.com/v1",
      "api_key_required": true,
      "implementation_status": "protocol_supported",
      "source_url": "https://github.com/openai/openai-go/blob/d5292055f5d00d99d949017745a81d31cd186222/README.md",
      "source_ref": "d5292055f5d00d99d949017745a81d31cd186222",
      "source_license": "Apache-2.0"
    }
  ]
}
```

上例仅节选一项；实现固定返回六项。前端严格验证字段、数量、唯一 ID、协议/状态、URL 与固定 40 位 GitHub SHA；畸形响应不自动应用。来源链接只在本人点击时打开，使用 `noopener noreferrer` 和 `no-referrer`，不会向其传密钥。

实现及契约：[后端注册表](<../internal/api/model_presets.go>)、[客户端](<../apps/desktop/src/model-config-api.ts>)、[OpenAPI](<../api/openapi.yaml>)。已有 `GET/PATCH /api/v1/ai/model-config` 仍是独立的人工配置接口，不能由预设读取替代。

## 4. Agent 业务 API 和 MCP

内置 Assistant 目录当前列出 **73 个业务 API：53 个可执行工具、7 个人工专用、13 个尚未实现**；另列 5 个运行/确认控制 API。新增 `get_model_presets` 是只读工具，不接受目标、模型 ID 或凭据，也不探测远端。读取连接目录仅代表本机缓存状态，不能宣称平台当前在线。

凭据配置与设备授权仍由人操作；模型不能读取密钥、自由请求 URL、执行文件/SQL/操作系统命令或绕过确认。只读及写工具以 [Assistant 目录](<../internal/api/assistant_catalog.go>) 的实际可用性为准，不能把目录条目总数宣传为全部已开放权限。

外部 MCP 仍为 **6 个工具**，未增加成任意 API 代理：

- `search_conversations` 新增严格可选 `conversation_kind: "private" | "group"`；省略包含全部类型，空串和其他值拒绝。
- `get_conversation_messages` 新增正文 `q`，与现有会话搜索一样使用最多 256 个 UTF-8 字节的字面关键词；`%`、`_` 不作 SQL 通配符，中文按字面、ASCII 字母不区分大小写；MCP 参数额外沿用无控制字符要求。
- 未知/重复字段和不合法类型拒绝；分页游标与查询、会话、筛选和工作区版本绑定，更换条件应重新查询。仅空白关键词按无关键词处理；命中历史摘要不替代真实最近一条消息。
- 源微信只读与导入后本地写入的区别、暂停保护、自动整理可能外发给已授权模型、通用聊天发送未开放等原有边界不变。

外部 MCP 的凭据/本机连接说明见 [MCP 设置](<mcp-setup.md>)；服务实现见 [六工具定义](<../internal/mcp/server.go>)。Assistant 与外部 MCP 是不同入口，不可把 53 个内置可执行工具称为 53 个 MCP 工具。

## 5. GitHub 来源与许可

仅核对公开资料及配置，不新增上游 SDK 依赖，不执行上游代码，不复制其模型 ID 或供应商商标/图片。下列许可仅适用于所链接来源，不授予品牌或服务权利。

| 来源 | 固定 ref / 版本 | 来源许可 |
|---|---|---|
| [OpenAI Go README](<https://github.com/openai/openai-go/blob/d5292055f5d00d99d949017745a81d31cd186222/README.md>) | `d5292055f5d00d99d949017745a81d31cd186222` / v3.102.0 | Apache-2.0 |
| [DeepSeek 官方集成资料](<https://github.com/deepseek-ai/awesome-deepseek-integration/tree/172dc8b74a0d549f4bce9850f7fe6a25b37a5ac7>)；另核对 [Chat Completions 文档](<https://api-docs.deepseek.com/api/create-chat-completion/>) | `172dc8b74a0d549f4bce9850f7fe6a25b37a5ac7` | CC0-1.0 |
| [Gemini 官方 OpenAI compatibility cookbook](<https://github.com/google-gemini/cookbook/blob/853a69ac49e21a917e7cdd178cadac193652c627/quickstarts/Get_started_OpenAI_Compatibility.ipynb>) | `853a69ac49e21a917e7cdd178cadac193652c627` | Apache-2.0 |
| [Qwen-Agent 官方示例](<https://github.com/QwenLM/Qwen-Agent/blob/31a4d36d123688581a9e9744427272b33ce940e0/examples/assistant_qwen3_coder.py>) | `31a4d36d123688581a9e9744427272b33ce940e0` | Apache-2.0 |

Web 图标独立来自固定 Lucide 子集，来源及完整许可见 [Lucide 资产说明](<../third_party/lucide/README.md>)；没有为减少文字而加入无关照片、插图或品牌素材。
