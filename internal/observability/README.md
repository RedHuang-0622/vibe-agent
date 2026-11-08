# internal/observability

## Role

从 Session Event Ledger 构建 trace、工具调用统计、失败原因、耗时分位数和 retrieval metadata 读模型。

## API / Methods

导出 `Trace`、`BuildStats`、`ProjectionPaths`、`DefaultProjectionPaths`、
`WriteProjections` 和 `LoadStats`。查询结果携带 watermark、tool kind/mode、
失败原因、in-flight/orphaned 和 avg/P50/P95。

## Boundaries

- 只读事件真相源，不能修改 Session。
- 投影可删除重建。
- wall clock 不参与顺序判断，耗时使用事件中的 monotonic 结果。
- in-flight 必须由当前 active call 集显式传入；无法证明仍在执行时标记 orphaned。
- 自由文本错误不得作为 failure reason 主键。

## Verification

```text
go test -count=1 ./internal/observability/...
```
