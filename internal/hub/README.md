# internal/hub

## Role

Hub 控制面、loopback admin/agent API、Account 读写路由、Session 进程表、
spawn/stop/reattach、generation 恢复和跨会话 Knowledge 路由。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `CanInvokeAccountMethod()` | function | Hub 可调用全部已知 Account 读/写/诊断方法 |
| `Manager` / `NewManager` | type / constructor | Session 进程表、创建、查询、路由、停止和崩溃重启 |
| `Server` / `NewServer` | type / constructor | loopback HTTP control API 与 token scope |
| `SessionManager` | interface | Hub server 消费的最小会话控制端口 |

## Boundaries

- Hub 不写 Session 事件。
- Hub AccountAdmin 可读写；AccountReader 只读。
- 写操作必须校验 revision 并审计。
- 审计身份包含调用角色与 Hub request id；不记录 secret。
- agent token 只允许 Account read/resolve、credential redeem 和 Knowledge；
  所有管理/session 控制方法需要 admin token。
- Hub 不启动 goroutine Actor；每 Session 仍是独立 `sessiond.exe`。
- reattach 必须同时校验 session metadata 与 health 的 session_id、pid、generation 和协议版本；stop 先等待进程退出再强制终止。

## Verification

```text
go test -count=1 -v ./internal/hub/...
```
