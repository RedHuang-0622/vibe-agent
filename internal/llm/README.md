# internal/llm

## Role

基于 AccountReader 与短期 lease 的 chat/tool-call、embedding 和 rerank
provider adapter。负责 capability 选择、响应归一化、向量维度/有限值校验
和稳定错误映射；binding timeout 覆盖请求与响应读取。search/weather 由工具
adapter 复用 `ResolveRemote`。

DeepSeek chat 请求发送 `model=deepseek-flash`、`messages`、`stream=false`，
并按 binding 附带 `thinking:{type}` 与 `reasoning_effort`。未设置 temperature
时不发送该字段，避免给 reasoning 请求增加无用或冲突参数。
OpenAI-compatible 工具续轮使用标准 `assistant.tool_calls[].function` 和
`role=tool/tool_call_id` 消息形状。
`anthropic_messages_v1` binding 支持 Anthropic Messages 的 system/messages/
tool_use/tool_result 转换、`anthropic-version` header、input_schema 和 usage 归一化。
Embedding 按 account binding 的 `batch_size` 分批发送，并附带配置的
`dimensions`；Rerank 发送 model/query/documents/top_n。

## API / Methods

导出 `Gateway`、`Request`、`Response`、`Message`、`ToolCall`、
`ToolDefinition`、`EmbeddingBatch`、`RerankRequest`、`RerankResponse`、
`Reader`、`LeaseRedeemer` 和 `RemoteConfig`。`NewGateway` 构造显式客户端。

## Boundaries

- 按 capability 解析 account binding。
- 只使用 AccountReader，不修改账户。
- secret 只通过 credential lease 在调用瞬间解析。
- 不把 provider SDK 类型泄漏到领域层。
- 不记录 Authorization header、credential lease 或 secret。
- provider 原始 body 和自由文本不得进入业务主键或 Session 事件。

## Verification

```text
go test -count=1 ./internal/llm/...
```
