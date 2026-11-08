# cmd/hub

## Role

`hub.exe` 的进程入口和控制面装配点。Hub 负责会话进程表、Sessiond 生命周期、Account 管理读写、KnowledgeService、TUI/CLI 路由和跨会话查询。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `main()` / `run()` | entrypoint | 启动 Hub 或执行 status/session/chat/tool/compress/trace/stats/account CLI |

## Boundaries

- 不直接写 Session 的 `messages.jsonl`。
- 对外管理操作需要 operator/account 权限。
- Sessiond 只能通过只读 Account surface 访问账户。

## Verification

```text
go test ./internal/hub/...
go test ./internal/operator/...
go build ./cmd/hub
```
