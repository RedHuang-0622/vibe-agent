# Account Provider Configuration

## 1. Scope

本文说明 vibe_agent Account 的 provider 入参、DeepSeek Flash 默认装配，以及如何把
同类配置映射到 Claude Code。研究时间：2026-11-04。

官方参考：

- [DeepSeek Chat Completions](https://api-docs.deepseek.com/api/create-chat-completion)
- [DeepSeek Models & Pricing](https://api-docs.deepseek.com/quick_start/pricing)
- [DeepSeek Claude Code Integration](https://api-docs.deepseek.com/quick_start/agent_integrations/claude_code)
- [Claude Code settings](https://code.claude.com/docs/en/settings)
- [Claude Code environment variables](https://code.claude.com/docs/en/env-vars)
- [Claude Code model configuration](https://code.claude.com/docs/en/model-config)

## 2. DeepSeek Flash 默认装配

`provider=deepseek` 的 `chat`/`tool_call` binding 使用以下安全默认值：

| 字段 | 默认值 |
|---|---|
| protocol | `openai_compatible` |
| base_url | `https://api.deepseek.com` |
| path | `/chat/completions` |
| model | `deepseek-flash` |
| credential_ref | `env:DEEPSEEK_API_KEY` |
| timeout | 120 秒 |
| thinking.type | `enabled` |
| reasoning_effort | `high` |

因此最小账户只需：

```json
{"schema_version":1,"account_id":"deepseek-flash","revision":1,"enabled":true,"display_name":"DeepSeek Flash","priority":100,"default_for":["chat","tool_call"],"services":{"chat":{}}}
```

DeepSeek chat binding 会自动复用到缺失的 tool_call binding；需要不同 thinking
策略时可显式覆盖 `services.tool_call`。

仓库提供可直接导入的最小文件：

```powershell
hub.exe llm account add --file config/deepseek-account.example.jsonl
```

DeepSeek `chat`/`tool_call` 的 model 固定为 `deepseek-flash`。显式写
`deepseek-v4-pro`、`deepseek-chat` 或 Claude Code 的 `deepseek-flash[1m]`
会 validate 失败；`[1m]` 只属于 Claude Code 的本地模型别名语义，不是
OpenAI-compatible `/chat/completions` 的 model ID。

## 3. 思考强度

当前官方规范：

- `thinking.type`: `enabled` / `disabled`
- `reasoning_effort`: `none` / `low` / `high` / `max`
- 兼容别名：`minimal -> low`，`medium/xhigh -> high`

用户配置会规范化后再保存和展示：

| 配置 | 规范化结果 |
|---|---|
| 不写 thinking/effort | `enabled + high` |
| 只写 `reasoning_effort=max` | `enabled + max` |
| 只写 `reasoning_effort=none` | `disabled + 空 effort` |
| `thinking.disabled` | `disabled + 空 effort` |
| `thinking.enabled + effort=none` | validate 失败 |
| `thinking.disabled + effort=high` | validate 失败 |

vibe_agent 实际发送：

```json
{
  "model": "deepseek-flash",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello!"}
  ],
  "thinking": {"type": "enabled"},
  "reasoning_effort": "high",
  "stream": false
}
```

Authorization 使用短期 credential lease 解析出的
`Authorization: Bearer <DEEPSEEK_API_KEY>`。API key 不写入 Account JSONL、
AccountView、Session 事件或日志。

## 4. Chat / Tool Call 示例

```json
{
  "schema_version": 1,
  "account_id": "deepseek-flash-high",
  "revision": 1,
  "enabled": true,
  "display_name": "DeepSeek Flash High",
  "priority": 100,
  "default_for": ["chat", "tool_call"],
  "services": {
    "chat": {
      "api_key_env": "DEEPSEEK_API_KEY",
      "thinking": {"type": "enabled"},
      "reasoning_effort": "high"
    },
    "tool_call": {
      "api_key_env": "DEEPSEEK_API_KEY",
      "thinking": {"type": "enabled"},
      "reasoning_effort": "high"
    }
  }
}
```

`api_key_env: NAME` 会规范化为 `credential_ref: env:NAME`。也可直接使用
`credential_ref`，但不得填写明文 key。

### 4.1 Anthropic Messages Binding

除 `openai_compatible` 外，chat/tool_call binding 支持
`protocol: anthropic_messages_v1`。适用于 Anthropic Messages-compatible 网关：

```json
{
  "capability": "tool_call",
  "provider": "anthropic-compatible",
  "protocol": "anthropic_messages_v1",
  "base_url": "https://provider.example/anthropic",
  "path": "/v1/messages",
  "model": "<provider-model>",
  "credential_ref": "env:ANTHROPIC_AUTH_TOKEN",
  "timeout": "120s"
}
```

Gateway 发送 `Authorization: Bearer`、`anthropic-version: 2023-06-01`，把
system 消息合并为 `system`，把模型 tool call 转为 `tool_use`，把工具结果转为
`tool_result`，并从 `content[]` 还原 text/tool_use。OpenAI-compatible 续轮同时
使用标准 `assistant.tool_calls` 与 `role=tool` 消息，不使用内部 ToolCall 扁平
结构直接发送。

真实链路冒烟为显式 opt-in：

```powershell
$env:ANTHROPIC_AUTH_TOKEN = '<valid provider token>'
$env:ANTHROPIC_BASE_URL = 'https://provider.example/anthropic'
$env:ANTHROPIC_MODEL = '<provider-model>'
powershell -File scripts/tui-real-api-smoke.ps1 -RunRealApi -BinDir bin
```

若环境提供 `DEEPSEEK_API_KEY`，同一脚本优先使用 OpenAI-compatible
DeepSeek Flash 路径；否则才读取上述 Anthropic 环境变量。

脚本通过隐藏 PTY 驱动 TUI，要求模型执行 todo_board 的
create/list/update/get/delete；鉴权失败记 BLOCKED，不产生 PASS。

## 5. Embedding

DeepSeek 当前公开 API 目录没有为本项目提供 embedding/rerank capability，因此这两类
binding 保持 provider-agnostic，不伪装成 DeepSeek 官方能力。

`openai_embeddings_v1` 请求：

```json
{
  "model": "your-embedding-model",
  "input": ["alpha", "beta"],
  "dimensions": 1536
}
```

响应：

```json
{
  "data": [
    {"embedding": [0.1, 0.2]},
    {"embedding": [0.3, 0.4]}
  ],
  "model": "your-embedding-model",
  "usage": {"total_tokens": 4}
}
```

账户配置：

```json
{
  "schema_version": 1,
  "account_id": "embedding-main",
  "revision": 1,
  "enabled": true,
  "display_name": "Embedding Main",
  "default_for": ["embedding"],
  "services": {
    "embedding": {
      "provider": "openai-compatible",
      "protocol": "openai_embeddings_v1",
      "base_url": "https://your-embedding-provider.example/v1",
      "path": "/embeddings",
      "model": "your-embedding-model",
      "api_key_env": "EMBEDDING_API_KEY",
      "timeout": "60s",
      "dimensions": 1536,
      "batch_size": 32,
      "normalize": true
    }
  }
}
```

约束：

- 返回向量数必须等于本批 input 数；
- 每条向量维度必须等于 `dimensions`；
- 向量必须全部 finite；
- `batch_size` 控制 HTTP 请求分批；
- query/document 使用同一 embedding fingerprint。

## 6. Rerank

`json_rerank_v1` 请求：

```json
{
  "model": "your-rerank-model",
  "query": "Go context cancellation",
  "documents": ["context cancellation stops work", "unrelated text"],
  "top_n": 2
}
```

响应：

```json
{
  "results": [
    {"index": 0, "relevance_score": 0.93},
    {"index": 1, "relevance_score": 0.21}
  ],
  "model": "your-rerank-model"
}
```

账户配置：

```json
{
  "schema_version": 1,
  "account_id": "rerank-main",
  "revision": 1,
  "enabled": true,
  "display_name": "Rerank Main",
  "default_for": ["rerank"],
  "services": {
    "rerank": {
      "provider": "your-rerank-provider",
      "protocol": "json_rerank_v1",
      "base_url": "https://your-rerank-provider.example/v1",
      "path": "/rerank",
      "model": "your-rerank-model",
      "api_key_env": "RERANK_API_KEY",
      "timeout": "30s",
      "top_n": 8,
      "max_candidates": 50
    }
  }
}
```

要求 `top_n <= max_candidates`。响应 index 必须唯一且在候选范围内，score
必须 finite；strict 模式漏项会返回 `upstream_schema_error`。

## 7. Claude Code 对照

Claude Code 的官方配置不是 YAML，而是严格 JSON + 环境变量：

- 用户：`~/.claude/settings.json`
- 项目共享：`.claude/settings.json`
- 项目本地：`.claude/settings.local.json`
- 管理端：`managed-settings.json`

Claude Code 不支持 JSON 注释或 trailing comma。若要使用 YAML，只能作为团队
外部的生成源，最终仍应生成严格 JSON；vibe_agent 不引入 YAML parser。

DeepSeek 官方 Claude Code 接入使用：

- `ANTHROPIC_BASE_URL=https://api.deepseek.com/anthropic`
- `ANTHROPIC_AUTH_TOKEN=<DEEPSEEK_API_KEY>`
- `ANTHROPIC_MODEL=deepseek-flash[1m]`
- `CLAUDE_CODE_EFFORT_LEVEL=max`

仓库提供不含 secret 的 settings 示例：

`config/claude-code-deepseek.settings.example.json`

Windows PowerShell 仍需在启动 Claude Code 前注入 token：

```powershell
$env:ANTHROPIC_AUTH_TOKEN = $env:DEEPSEEK_API_KEY
claude --settings config/claude-code-deepseek.settings.example.json
```

`ANTHROPIC_API_KEY` 会使用 `X-Api-Key`，`ANTHROPIC_AUTH_TOKEN` 会使用
`Authorization: Bearer`。DeepSeek 官方 Claude Code 文档使用后者。

## 8. YAML / JSONL 决策

vibe_agent 的账户文件继续使用严格 JSONL：

- 一行一个完整 account JSON object；
- 标准库可直接读写；
- 支持原子替换和 revision；
- 不新增 YAML 解析依赖；
- 与 Claude Code 的严格 JSON 策略一致。

如需 YAML 作者体验，可在仓库外先转换为 JSON/JSONL；运行时不保存 YAML。
