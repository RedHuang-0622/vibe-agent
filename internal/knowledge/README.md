# internal/knowledge

## Role

Memory record/chunk 持久化、自研 BM25/CJK tokenizer、embedding dense index、
RRF 融合、rerank 和 strict/degraded hybrid retrieval。Hub 是共享 KnowledgeService 的所有者，
Sessiond 仅通过部署查询接口使用。

## API / Methods

导出 `Service`、`Open`、`Config`、`DefaultConfig`、`ServiceError`、
`EmbeddingClient`、`RerankClient`、`Tokenizer`、`DefaultTokenizer`、
`BM25Index`、`LexicalDocument` 和 `LexicalHit`。

- `Service.Write` / `Search` / `Update`
- `BM25Index.Replace` / `Upsert` / `Delete` / `Search`

## Boundaries

- BM25 必须由项目自行实现。
- 查询和文档向量必须使用同一 embedding fingerprint。
- 索引使用 copy-on-write/原子 snapshot。
- Memory tool 事件由 Sessiond Tool Runtime 记录，不由本包写 Session 事件。
- strict 模式不得静默降级；degraded 结果必须带 `partial_result` warning。
- BM25 默认中文 tokenizer 版本由 `cjk-uni-bigram-v1` 固定，变更必须重建索引。

## Verification

```text
go test -count=1 ./internal/knowledge/...
go test -race -count=3 ./internal/knowledge/...
```
