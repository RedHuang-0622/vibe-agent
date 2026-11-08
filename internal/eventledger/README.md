# internal/eventledger

## Role

负责单个 Session 的 `messages.jsonl` 追加式真相源、连续 `seq` 发放、
事件身份、尾部未提交行隔离和只读重放。Hub、工具和投影均不是本包写者。

## API / Methods

导出常量 `SchemaVersion`、事件类型与 visibility；类型 `Draft`、`Event`、
`Snapshot`、`Options`；函数 `DefaultOptions`、`EventID`、`OpenWriter`、
`ReadFile`；`Writer` 提供 `Append`、`ReadRange`、`LastCommitted`、
`ChangeSignal`、`Close`。

## Boundaries

- 只由 Sessiond 的单一 Actor/写者持有写入能力。
- 顺序只由 seq 决定；时间只用于审计。
- 原始事件只追加，不重写；投影不得成为第二事实源。
- 完整但非法的历史行立即失败；只有末尾未带换行的半行可先备份后修复。
- `ReadRange` 与 `Append` 共享写者互斥，查询不会看到半写入事件。
- `ChangeSignal` 只在 flush/fsync 成功并推进内存水位后关闭；它只用于
  唤醒观察者，不携带可替代 `LastCommitted` 的顺序值。

## Verification

```text
go test -count=1 ./internal/eventledger/...
go test -race -count=3 ./internal/eventledger/...
```
