# internal/tooling

## Role

工具规格、Registry、JSON Schema 子集校验、模型/直调统一运行时、结构化异常
和 monotonic 调用耗时。具体工具位于 `tools/<name>`。

## API / Methods

导出 `Registry`、`Runtime`、`Call`、`Result`、`Error`、`Handler`、
`DirectAdapter`、`Authorizer`、`RemoteConfig`；提供 `NewRegistry`、
`NewRuntime`、`HTTPError`、`DecodeJSONObject` 及稳定错误码常量。

## Boundaries

- 模型 tool_call 与 `/toolname req` 共用 Registry/Tool Runtime。
- 具体工具不直接写 Event Ledger，`Runtime` 只返回结果。
- 有副作用工具必须支持幂等键。
- quota/timeout/auth 等错误使用稳定 code。
- 参数校验只承诺项目需要的 JSON Schema 子集。
- 不允许包级可变状态或全局 Registry。

## Verification

```text
go test -count=1 ./internal/tooling/...
go test -race -count=3 ./internal/tooling/...
```
