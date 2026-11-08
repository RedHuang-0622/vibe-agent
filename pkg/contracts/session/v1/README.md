# pkg/contracts/session/v1

## Role

定义 Session 控制面、事件视图、trace/stats 查询和 Hub/Sessiond frame 的稳定 DTO。

## API / Methods

导出类型包括 `SessionRef`、`SessionView`、`SessionState`、控制请求/响应、
`EventView`、`WatchRequest`、`SessionSnapshotRequest/Response`、
`SessionHistoryRequest/Response`、trace/stats DTO、`HealthResponse`、`Frame`、`FrameResponse`、
`HTTPRequest` / `HTTPResponse` 和带 reset 信息的稳定错误 DTO。
导出常量包括 `ContractName`、`Version`、`ProtocolVersion` 和
`MethodSession*`。

## Boundaries

- 不包含 Session 聚合、Actor 或 Event Ledger 实现。
- 不包含文件路径、账户 secret 或 provider 原始错误。
- 本包不得 import `internal/*`。
- 破坏性字段变更必须创建 v2。
- `session.snapshot.tail_limit` 只返回最新页；`has_more_before` 表示更早事件存在。
- `session.history.before_seq` 返回严格早于该 seq 的升序页；`resync_required`
  表示调用方 cursor 已不可信，应重新快照。
- `session.watch.after_seq` 是显式应用游标；调用方应用完当前页后才可把
  `cursor` 用于下一次请求。`timed_out=true` 只表示等待窗口内没有新事件。
- `resync_required=true` 时不得猜测缺口内容，必须重新读取 snapshot。
- 新字段均为 v1 additive 字段，未改变既有事件和错误语义。

## Verification

```text
go test -count=1 ./pkg/contracts/session/v1
go list -deps ./pkg/contracts/session/v1
```
