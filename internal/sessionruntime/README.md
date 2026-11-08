# internal/sessionruntime

## Role

Hub/Sessiond 长度前缀 JSON IPC、本地客户端、metadata、Hub HTTP client 和
generation/endpoint 交接原语。

## API / Methods

导出 `WriteFrame`、`ReadFrame`、`Server`、`Handler`、`Client`、
`HubClient`、`Metadata`、`WriteMetadata`、`ReadMetadata`、
`RandomToken` 和稳定 `CodedError`。

## Boundaries

- Hub 不直接写 Session 事件。
- Sessiond 业务连接不能用父子 stdio。
- Sessiond 只服务一个 Session。
- 进程状态由 Hub ProcessManager 管理，不用每会话协程代替。
- Sessiond 端点只绑定 loopback，随机 token 校验，帧上限 8 MiB。
- 客户端与服务端校验 request/response id、协议版本；CodedError 保留稳定 code、retry、reset 和脱敏 request id。
- `sessiond.json` 不是业务契约，不得被 TUI/CLI 直接读取。

## Verification

```text
go test -count=1 ./internal/sessionruntime/...
```
