# cmd/sessiond

## Role

`sessiond.exe` 的进程入口。每个活跃 Session 由一个独立 Sessiond 进程服务，拥有该会话的 Actor、ReAct Loop、压缩、工具运行时、LLM 客户端和事件单写者。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `main()` | entrypoint | 解析 Session/generation/Hub 参数并运行 `sessiond.Run` |

## Boundaries

- 不管理其他 Session。
- 不写 Account 配置，只允许读取调用所需的 Account binding/lease。
- 不直接访问 Account JSONL adapter。
- 不得绕过 Actor 写事件或发放 seq。

## Verification

```text
go test ./internal/sessiond/...
go test ./internal/conversation/...
go build ./cmd/sessiond
```
