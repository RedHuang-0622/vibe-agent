# internal/sessiond

## Role

单 Session 数据面：会话锁、Event Ledger 单写者、Actor、ReAct、工具/模型/压缩/
投影查询、本地 IPC server 和 Hub 只读 Account/Knowledge client。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `CanInvokeAccountMethod()` | function | 只允许 Account read get/resolve |
| `Run()` | function | 装配并运行一个 Sessiond |
| `Config` | type | Session、generation、Hub 与运行时参数 |

Handler 还实现 `session.events`、`session.watch`、`session.snapshot` 和
`session.history`：事件页返回 `cursor/has_more/resync_required`，watch 最多
等待 30 秒并由持久化 append 通知唤醒，snapshot 返回最新尾页和进程状态，
history 返回 `before_seq` 之前的一页。

默认 Registry 同时注册旧 `todo` 与会话级 `todo_board`；后者持久化到
`<session-dir>/todo-board.jsonl`，模型与 slash 均通过统一 Tool Runtime。

## Boundaries

- 禁止 Account 写、admin list、validate、smoke。
- 禁止 import `internal/hub` 和 `internal/accounts/adapters`。
- 只读 binding/lease 用于 provider 调用。
- 每个进程只服务一个 Session，并独占 `messages.jsonl` 写入权。
- IPC token 随机生成；业务端点只绑定 loopback。
- health 返回 generation，Hub 用它阻止旧进程或错误 metadata 被重新接管。
- snapshot 的 process state 不能翻译成 turn running；运行态只由 `turn.*`
  事件确认。
- 长轮询不创建第二写者，事件仍只由 Actor/Event Ledger 提交。

## Verification

```text
go test -count=1 -v ./internal/sessiond/...
```
