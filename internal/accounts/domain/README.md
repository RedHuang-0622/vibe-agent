# internal/accounts/domain

## Role

Account 聚合、能力枚举和绑定不变量。该包不依赖公共 wire DTO、Hub、文件系统或 credential 实现。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `Capability.Valid()` | method | 判断 capability 是否受支持 |
| `Account.Validate()` | method | 校验 ID、revision、archived/enabled、binding、默认能力、DeepSeek thinking/effort、embedding/rerank 参数和超时 |
| `NormalizeBinding()` | function | 补 DeepSeek Flash 默认值并规范化 thinking/effort |
| `NormalizeServices()` | function | 规范化全部 binding，并把 DeepSeek chat 复用到缺失的 tool_call |
| `NormalizeThinkingType()` / `NormalizeReasoningEffort()` | functions | 官方取值和兼容别名规范化 |

主要类型：`Account`、`Binding`、`Capability`；`Binding` 包含 normalize、
dimensions、batch size 和 rerank 候选限制。
DeepSeek chat/tool_call 默认 `deepseek-flash`、`env:DEEPSEEK_API_KEY`、
thinking enabled 和 high effort；规范 effort 为 `none/low/high/max`。
常量：`CapabilityChat`、`CapabilityToolCall`、`CapabilityEmbedding`、`CapabilityRerank`、`CapabilitySearch`。

## Boundaries

- 不保存 secret，只保存 `CredentialRef`。
- 不 import account v1 public DTO。
- Account 写操作只能由 Hub application 侧调用。

## Verification

```text
go test ./internal/accounts/domain
```
