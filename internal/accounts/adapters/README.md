# internal/accounts/adapters

## Role

Hub 内部的 Account JSONL Repository 与追加审计 adapter。

## API / Methods

导出 `OpenJSONLRepository`、`JSONLRepository`、`NewFileAudit`、`FileAudit`。
同时导出 `NewHTTPDemoSmokeRunner` 与 `HTTPDemoSmokeRunner`。

- `JSONLRepository` 实现 `app.Repository`，接受 `timeout`/`timeout_ms`、
  `api_key_env`、`thinking` 与 `reasoning_effort`，拒绝未知 JSON 字段和重复账户
- `FileAudit` 实现 `app.AuditWriter`
- `HTTPDemoSmokeRunner` 实现 `app.SmokeRunner`，消费用例 input/timeout 并校验 provider 响应形状

## Boundaries

- Sessiond 禁止 import 本包，也只能由 Hub composition root 构造。
- 不导出真实 secret 或原始账户文件句柄。
- 账户更新必须使用临时文件 + 原子替换。
- DeepSeek 默认值在加载时补齐；持久化只保存 CredentialRef，不保存 key。
- 审计记录不得包含 credential value、Authorization header 或 provider body。

## Verification

```text
go test -count=1 ./internal/accounts/adapters/...
```
