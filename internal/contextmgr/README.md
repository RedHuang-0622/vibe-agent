# internal/contextmgr

## Role

基于事件 seq 的安全压缩红线、摘要服务、checkpoint 和有效上下文重建。

## API / Methods

导出 `SafeCutoff`、`CutoffInput`、`ErrNoSafeCutoff`、`Build`、`ContextView`、
`Checkpoint`、`SourceDigest`、`Summarizer` 和 `BasicSummarizer`。

## Boundaries

- 压缩红线必须绑定事件 seq。
- 不得切断未完成 tool call 或吞掉未消费消息。
- 本包不 append Event Ledger；压缩请求/完成/失败事件由 Session Actor 提交。
- 压缩失败不得推进有效水位。
- checkpoint 必须带 source digest；digest 不匹配时回退到之前的合法 checkpoint。
- 安全边界不得停在 assistant 消息后、turn 完成前。

## Verification

```text
go test -count=1 ./internal/contextmgr/...
```
