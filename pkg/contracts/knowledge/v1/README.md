# pkg/contracts/knowledge/v1

## Role

定义 memory 混合检索的跨模块 DTO，包括词法、稠密、rerank 通道及返回命中结构。

## API / Methods

导出类型包括 `Channel`、`Query`、`Hit`、`Result`、`Warning`、`Retrieval`、
`WriteRequest`、`UpdateRequest` 和 `RecordRef`；通道常量为 `ChannelBM25`、
`ChannelDense`、`ChannelRerank`。

## Boundaries

- 不包含 BM25 算法、provider SDK 或索引实现。
- 本包不得 import `internal/*`。
- `Hit` 必须能表达各通道排名、RRF 分数、rerank 分数和降级状态。
- embedding 向量不应进入普通公共 hit DTO。

## Verification

```text
go test -count=1 ./pkg/contracts/knowledge/v1
go list -deps ./pkg/contracts/knowledge/v1
```
