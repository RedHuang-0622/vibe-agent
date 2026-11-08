# cmd/tui

## Role

`tui.exe` 的终端客户端入口。通过 Hub API 展示会话、聊天、工具状态、trace、统计、压缩和账户管理界面。

## API / Methods

| Name | Kind | Purpose |
|---|---|---|
| `main()` | entrypoint | 幂等拉起 Hub 并启动 Hub-only interactive client |

## Boundaries

- 不直接连接 Sessiond。
- 不直接读取 `messages.jsonl`、Account JSONL 或 secret。
- 所有管理/聊天操作经 Hub 权限校验。
- 启动和切换会话先读 `session.snapshot`；`/history`、`/older` 使用有界
  tail/older 窗口并按 seq 去重。
- 回合等待使用可重连的 `session.watch`；`resync_required` 时重新快照，
  不把最后一帧伪装成在线状态。

## Verification

```text
go build ./cmd/tui
go test ./internal/operator/...
```
