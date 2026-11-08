# 账户与 API 冒烟配置

## 文件

- `llm-accounts.example.jsonl`：账户模板，每行一个完整账户。
- `deepseek-account.example.jsonl`：Chat/Tool Call 最小 DeepSeek Flash 账户。
- `embedding-rerank.example.jsonl`：可直接导入的 embedding/rerank provider 模板。
- `claude-code-deepseek.settings.example.json`：Claude Code 官方设置格式示例，不含 secret。
- `llm-smoke.example.jsonl`：带标签的 API 冒烟用例；真实执行需显式传 `--tag real-api`。
- `llm-accounts.local.jsonl` / `llm-smoke.local.jsonl`：推荐的本地真实配置名，已在 `.gitignore` 中排除。

JSONL 规则：

- 一行一个 JSON object，不能有 JSON 注释和多行对象。
- 空行可忽略；加载器应报告非法 JSON 的行号。
- 同一文件内 `account_id` 唯一。
- 账户更新通过临时文件原子替换，不在真实文件中手工并发编辑。

## 填写步骤

1. 复制账户模板：

   ```powershell
   Copy-Item config/llm-accounts.example.jsonl config/llm-accounts.local.jsonl
   ```

   只接 DeepSeek Chat/Tool Call 时可直接使用最小模板：

   ```powershell
   hub.exe llm account add --file config/deepseek-account.example.jsonl
   ```

2. DeepSeek `chat` / `tool_call` 可以直接使用精简配置，未填字段自动使用
   `deepseek-flash`、`https://api.deepseek.com`、`/chat/completions`、
   `openai_compatible`、`env:DEEPSEEK_API_KEY` 和 120 秒超时：

   ```json
   {
     "capability": "chat",
     "api_key_env": "DEEPSEEK_API_KEY",
     "thinking": {"type": "enabled"},
     "reasoning_effort": "high"
   }
   ```

   `thinking.type` 支持 `enabled` / `disabled`；`reasoning_effort` 支持
   `none` / `low` / `high` / `max`，并兼容 `minimal -> low`、
   `medium/xhigh -> high`。只写 `none` 会关闭 thinking。DeepSeek
   chat/tool_call 的模型固定为
   `deepseek-flash`，显式填写其他 DeepSeek 模型会被 validate 拒绝。

3. 为 embedding/rerank/search 替换相应的 `base_url`、`path`、`model` 和
   `dimensions`。
4. 凭据只引用环境变量名，不把 key 写进文件：

   ```powershell
   $env:DEEPSEEK_API_KEY = '<set in your environment>'
   $env:DEEPSEEK_SEARCH_API_KEY = '<set in your environment>'
   $env:EMBEDDING_API_KEY = '<set in your environment>'
   $env:RERANK_API_KEY = '<set in your environment>'
   ```

5. 校验结构：

   ```powershell
   hub.exe llm account validate --file config/llm-accounts.local.jsonl
   ```

6. 执行离线契约测试；随后显式执行真实冒烟：

   ```powershell
   hub.exe llm account smoke --file config/llm-smoke.example.jsonl --offline
   hub.exe llm account smoke --file config/llm-smoke.local.jsonl --tag real-api
   ```

Anthropic Messages-compatible chat/tool_call 可使用
`protocol: anthropic_messages_v1` 和
`credential_ref: env:ANTHROPIC_AUTH_TOKEN`。隐藏 PTY 的 TUI 工具链冒烟：

```powershell
$env:ANTHROPIC_AUTH_TOKEN = '<valid token>'
$env:ANTHROPIC_BASE_URL = 'https://provider.example/anthropic'
$env:ANTHROPIC_MODEL = '<provider-model>'
powershell -File scripts/tui-real-api-smoke.ps1 -RunRealApi -BinDir bin
```

若只提供 `DEEPSEEK_API_KEY`，脚本自动使用 OpenAI-compatible
`https://api.deepseek.com/chat/completions` 与 `deepseek-flash`。

默认 CI 使用 fake Anthropic-compatible provider 运行
`scripts/tui-toolchain-smoke.ps1`，真实调用必须显式 opt-in。

## 能力

| Capability | 关键字段 |
|---|---|
| `chat` | `api_key_env`、`thinking`、`reasoning_effort`；其余使用 DeepSeek 默认值 |
| `tool_call` | 同 chat，模型固定 `deepseek-flash` |
| `embedding` | model、dimensions、batch_size、normalize |
| `rerank` | model、top_n、max_candidates |
| `search` | endpoint、model/协议、credential_ref |

## 安全

- `api_key_env: NAME` 会规范化为 `credential_ref: env:NAME`；也可以直接写
  `credential_ref`。
- 密钥值只从环境变量/凭据存储读取，不写入 Account JSONL、Session 事件或日志。
- 不要把真实 key、authorization header、带签名的完整 URL 或 provider 原始错误 body 提交到仓库。
- smoke 报告只记录脱敏 request id、状态码、耗时和稳定错误码。
- 额度不足应保留 `remaining/limit/reset_at` 等非敏感元数据，便于后续请求决策。

## Reranker / Embedding 入参

- embedding 请求：`{"model":"...","input":["..."],"dimensions":N}`；
  响应使用 OpenAI-compatible `data[].embedding`。
- rerank 请求：
  `{"model":"...","query":"...","documents":["..."],"top_n":N}`；
  响应使用 `results[].index/relevance_score`。
- 完整字段、响应形状、维度/批次约束见
  [Account Provider Configuration](../docs/agent-runtime/14-account-provider-configuration.md)。

## YAML 策略

账户文件是严格 JSONL，不使用 YAML。Claude Code 当前也使用严格 JSON settings
文件和环境变量，不使用 YAML 账户文件。若团队希望用 YAML 维护，应在仓库外生成
本仓库需要的 JSON/JSONL，避免给运行时增加第二套配置语义和第三方解析依赖。
