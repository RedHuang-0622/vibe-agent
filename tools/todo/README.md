# tools/todo

## Role

维护本地持久化 Todo 条目与每会话独立的运维 Todo Board。旧 `todo` 工具保留
create/list/update/complete 兼容；`todo_board` 提供 list/get/create/update/delete、
状态流转、优先级、幂等键和 optimistic version。

## API / Methods

- `OpenStore(string) (*Store, error)`
- `New(*Store) *Tool`
- `Spec() toolv1.ToolSpec`
- `Tool.Invoke` 实现 `tooling.Handler`
- `Adapter.Adapt` 提供 slash 创建映射
- `OpenBoardStore(string) (*BoardStore, error)`
- `NewBoard(*BoardStore) *BoardTool`
- `BoardSpec() toolv1.ToolSpec`
- `BoardAdapter.Adapt` 提供 slash 创建映射
- 导出 `BoardItem`、`BoardSummary` 和 board status/priority 常量

## Boundaries

Todo 数据与 Session JSONL 分离；工具不写 Session 事件。写入使用临时文件和原子替换。
`todo_board.jsonl` 由 Sessiond 放在当前 Session 目录下，因此一个 board 只属于
一个 Session。create 必须带 `idempotency_key`；update/delete 还必须带最新读取
得到的 `expected_version`。删除保留 tombstone 供幂等重放，但 list/summary 不可见。
持久化失败时回滚内存 item 与 idempotency 映射，失败请求不能被误报为已完成。

## Verification

```powershell
go test -count=1 ./tools/todo
```
