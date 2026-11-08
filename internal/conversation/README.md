# internal/conversation

## Role

Session Actor、Turn/ReAct Loop、待发送消息队列、直接工具 turn、取消和压缩协调。

## API / Methods

导出 `Actor`、`Config`、`New`、`SubmitRequest`、`SubmitResponse`、
`DirectToolResult`、`CompressionResult`、`Status`、`QueuePolicy` 以及
`ModelClient`、`ToolExecutor`、`EventAppender`、`EventReader` 端口。

`Actor` 提供 `Start`、`Submit`、`DirectTool`、`Cancel`、`Compress`、
`Status`、`Close`。

## Boundaries

- Actor 是单会话串行调度语义，不是跨会话协程池。
- Tool/LLM 回调必须转为 Actor 命令。
- Actor 是会话事件写入的唯一调用入口；网络/工具回调回到命令通道。
- `turn.completed` 提交成功后，同一 Actor 命令内释放活动 turn 并推进队列。
- 不直接访问 provider SDK；模型与工具通过最小接口注入。
- 长 I/O 在独立 goroutine 中执行，但不得直接修改队列或事件账本。
- 每轮 system prompt 包含稳定工具使用策略与由 tool schema 生成的摘要；完整
  JSON Schema 仍通过 provider `tools` 字段发送，摘要不能替代参数校验。
- `todo_board` 提示要求先 list/get、再按 expected_version 更新/删除，并使
  用 idempotency_key；该提示只是模型引导，最终校验仍在 Tool Runtime/工具实现。

## Verification

```text
go test -count=1 ./internal/conversation/...
go test -race -count=3 ./internal/conversation/...
```
