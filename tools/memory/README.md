# tools/memory

## Role

把 `memory_write`、`memory_search`、`memory_update` 映射到统一 KnowledgeClient，承载 scope、幂等键、版本和 strict/degraded 请求。

## API / Methods

- `Client`
- `NewWrite(Client) *Tool`、`NewSearch(Client) *Tool`、`NewUpdate(Client) *Tool`
- `Spec(string) toolv1.ToolSpec`
- `Tool.Invoke` 实现 `tooling.Handler`
- `SearchAdapter`、`WriteAdapter`、`UpdateAdapter`

## Boundaries

不实现 BM25、向量索引或 provider；不直接读写 Memory 文件，不写 Session 事件。调用失败统一映射为 `tooling.Error`。

## Verification

```powershell
go test -count=1 ./tools/memory
```
