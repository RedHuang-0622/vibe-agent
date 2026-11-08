# internal/operator

## Role

CLI/TUI use cases、`/toolname req` 命令解析边界和显示模型 DTO。

## API / Methods

导出 `Client`，提供 session/chat/direct/cancel/compress/events/watch/
snapshot/history/trace/stats/tool/account/memory 用例；导出 `RunHubCLI`、
`WaitForTurn`、
`WaitForMessageTurn`、`RunTUI`、`LoadAccountFile`、`ValidateAccountFile`、
`LoadSmokeCases` 和 `SmokeCase`。

`WaitForMessageTurn` 按 `message_id -> turn_id` 关联本次提交；TUI 在切换
session、slash、cancel 与压缩后同步 watermark，避免旧 `turn.completed`
提前结束当前等待。

`WaitForMessageTurn` 使用 `session.watch` 和最后应用 cursor，可对可重试的
连接错误做有界指数退避重连；重复帧按 seq 跳过。TUI 的 tail/older 窗口由
子包 `sessioncache` 维护。CLI 新增 `events`、`snapshot`、`history`，用于
脚本化验证水位、分页和缺口语义。
TUI 在回合内显示 `tool.requested/completed/failed` 与 `llm.failed` 事件；
`/board` 经 Hub/sessiond 直调 `todo_board`，默认执行 list，也可传 JSON 操作。

## Boundaries

- TUI 只通过 Hub API。
- Slash 语法由服务端最终校验。
- 不读取 Session JSONL、Account JSONL 或 secret。
- `@file` 直调请求只读取工具参数文件，不作为账户配置入口。
- cursor 只能在事件被消费/显示后推进；`resync_required` 必须走 snapshot。
- `sessioncache` 是前端投影，不是会话事实源，也不跨会话保留正文。

## Verification

```text
go test -count=1 ./internal/operator/...
```
