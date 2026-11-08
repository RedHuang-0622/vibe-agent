# internal/accounts/app

## Role

Account 应用服务与最小端口，隔离 Hub Account application 与 JSONL、
credential broker、smoke runner 等基础设施。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `Repository.List` | interface method | 列出领域 Account |
| `Repository.Get` | interface method | 按 ID 读取 Account |
| `Repository.Save` | interface method | 按 expected revision 保存 Account |
| `LeaseIssuer.Issue` | interface method | 签发 invocation credential lease |
| `SmokeRunner.Run` | interface method | 按 account/capability/case/input/timeout 执行账户能力冒烟 |
| `Service` | accountv1.Admin implementation | Account 读写、revision、能力选择和审计 |
| `LeaseManager` | short-lived credential broker | 一次性 credential lease |
| `NewService` / `NewLeaseManager` | constructors | 显式依赖注入 |
| `WithRequestID` / `WithAuditIdentity` | function | audit request/actor 关联 |
| `ValidateCreate` | function | 对导入的 Account 配置做本地结构校验 |

chat/tool_call 绑定会把省略的 DeepSeek 配置规范化为 `deepseek-flash`、
`https://api.deepseek.com`、`/chat/completions`、thinking enabled 和 high
reasoning effort。`api_key_env` 转为 `credential_ref: env:<NAME>`。
未提供 credential ref 时默认 `env:DEEPSEEK_API_KEY`；effort 规范值为
`none/low/high/max`。只配置 DeepSeek chat 时自动生成等价的 tool_call binding。

## Boundaries

- 应用层不定义 HTTP/JSONL 细节。
- Sessiond 不得注入该完整管理应用。
- Repository 实现放 `internal/accounts/adapters`。
- secret 只存在于短期 lease manager 内存中，不得进入审计或日志。
- AccountView 可返回不含 credential 的 BindingView，用于展示 model/thinking/effort。
- 写操作必须携带 expected revision 并产生审计记录。
- 审计记录必须包含 actor、action、account_id、前后 revision、changed fields 和 request_id。

## Verification

```text
go test -count=1 ./internal/accounts/app/...
go test -race -count=3 ./internal/accounts/app/...
```
