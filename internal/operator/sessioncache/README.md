# internal/operator/sessioncache

## Role

保存一个 TUI 会话的有界事件窗口，处理最新快照、向前翻页、接缝去重、
增量水位和缺口重同步标记。它不是会话事实源，也不持有 hub、RPC 或持久化状态。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `Cache` / `New` | type / function | 创建无内部锁的前端视图缓存 |
| `ApplySnapshot` | method | 用最新页替换 tail，保留仍早于新 tail 的 older |
| `ApplyHistory` | method | 合并历史页并按 seq 去重 |
| `ApplyIncremental` | method | 追加实时/补读事件；检测 seq 空洞 |
| `Window` / `WindowFirstSeq` | methods | 读取升序有界窗口和翻页坐标 |
| `TurnState` | method | 读取仅由 `turn.*` 事件确认的运行态 |
| `ClearOlder` / `NeedsResync` | methods | 清理历史页、暴露重同步要求 |

## Boundaries

- 不读取 `messages.jsonl`，不调用 Hub/Sessiond。
- 不加锁；由 TUI Driver/Model 的单一所有者串行访问。
- 快照只替换 tail，不能把用户已翻到的 older 清空。
- 进程状态不能翻译成 turn running；快照后 turn 状态为 unknown。
- 检测到缺口时不猜测缺失事件，标记后由调用方重新快照。

## Verification

```text
go test -count=1 ./internal/operator/sessioncache
```
