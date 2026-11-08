# pkg/contracts/account/v1

## Role

Hub、Sessiond 和 TUI/CLI 共享的 Account v1 公共契约。该包只包含跨进程 DTO、能力枚举、method、错误码以及 Reader/Admin 接口，不包含领域实体、JSONL 存储实现或 secret。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `Capability.Valid()` | method | 校验 chat/tool_call/embedding/rerank/search |
| `Reader.Get` | interface method | 读取安全 AccountView |
| `Reader.ResolveForInvocation` | interface method | 解析一次调用所需的 binding 和 lease ID |
| `Admin` | interface | Reader 加账户管理写操作 |
| `Admin.List/Create/Update/SetDefault` | interface methods | Hub 管理读/写 |
| `Admin.SetEnabled/RotateCredentialRef/Archive` | interface methods | 账户生命周期写操作 |
| `Admin.Validate/Smoke` | interface methods | Hub 诊断能力 |
| `AllMethods()` | function | 返回 Account method 集合 |
| `IsAccountMethod()` | function | 判断是否为已知 Account method |
| `IsSessiondReadMethod()` | function | 判断 Sessiond 允许调用的只读方法 |
| `IsWriteMethod()` | function | 判断管理写方法 |
| `ContractError.Error()` | method | 输出稳定错误码与消息 |

主要 DTO：`AccountRef`、`AccountView`、`BindingView`、`ThinkingConfig`、
`ResolveRequest`、`ResolvedInvocation`、`CreateAccount`、`UpdateAccount`、
`SmokeRequest`、`ValidationReport`、`SmokeReport`。
`BindingView` / `BindingInput` 表达 embedding dimension、batch、normalize 和
rerank top_n/max_candidates，但不含 credential value。稳定错误码覆盖
`missing_credential`、`authentication_failed`、`rate_limited` 和
`quota_exhausted`。

DeepSeek chat/tool_call 的 `ThinkingConfig` 与 `reasoning_effort` 是安全配置，
会随 binding 解析到 Sessiond。`BindingInput.api_key_env` 只保存环境变量名，
不会进入 AccountView；`AccountView.Bindings` 可用于安全展示实际模型参数。
规范 effort 为 `none/low/high/max`；兼容 `minimal -> low` 和
`medium/xhigh -> high` 的归一化在账户应用/领域层完成。

`SmokeRequest` 可携带 JSONL 用例的 `input` 与可选 `timeout_ms`；fixture 位于
`testdata/smoke-request.json`，Hub Admin 与客户端均按 v1 JSON tag 解析。

## Boundaries

- 只能 import 标准库。
- 不得 import `vibe-agent/internal/*`。
- `BindingView` 不包含 `credential_ref` 或 secret。
- smoke input 只用于诊断请求，不得包含明文凭据。
- 破坏性变更必须发布 v2。

## Verification

```text
go test -count=1 ./pkg/contracts/account/v1
go list -deps ./pkg/contracts/...
```
