# pkg/contracts/tool/v1

## Role

定义工具调用的公共模式、结构化错误和耗时字段，供 Sessiond、Hub 查询面和客户端复用。

## API / Methods

导出类型包括 `ToolSpec`、`ListResponse`、`InvocationMode`、`Timing`、
`Error` 和 `Result`。
常量：`InvocationModeModel`、`InvocationModeDirect`。

## Boundaries

- 不包含 Registry、handler、命令解析或统计实现。
- 本包不得 import `internal/*`。
- `Timing` 使用毫秒整数传递；生产值必须来自 monotonic clock。
- `Error.Code` 必须使用稳定错误分类。

## Verification

```text
go test -count=1 ./pkg/contracts/tool/v1
go list -deps ./pkg/contracts/tool/v1
```
