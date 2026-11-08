# cmd/startup

## Role

`startup.exe` 的进程入口。负责登录启动项安装、检查和幂等拉起 `hub.exe`。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `main()` | entrypoint | 实现 `run`、`install`、`status`、`uninstall` |

## Boundaries

- 不启动 Sessiond。
- 不读写 Session 内容或事件。
- 不允许启动逻辑产生多个 Hub 实例。

## Verification

```text
go build ./cmd/startup
go test ./internal/platform/...
go test ./internal/bootstrap/...
```
