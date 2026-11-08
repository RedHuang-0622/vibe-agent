---
name: operator-cursor-and-smoke
description: Lessons from TUI watermark recovery, message/turn correlation and file-driven account smoke.
metadata:
  type: project
  tags: [go, tui, queue, account, smoke]
---

## 场景

TUI/CLI 在已有 turn、session 切换和 slash 直调后继续聊天；账户配置由示例 JSONL
和 smoke 用例驱动。

## 方案

Submit 返回 message_id 后，等待该 message 的 `user.message.consumed` 得到
turn_id，再只接受该 turn 的 terminal event。TUI 在 use/new/slash/cancel/compress
后重新读取 session watermark。Smoke CLI 支持 file/tag/offline，把 input 与
timeout 传入 Hub 管理面，真实 provider 调用保持显式 opt-in。

## 关键经验

- 等待“下一个 turn.completed”在排队场景不安全，可能返回上一个活动 turn。
- 切换 session 后 cursor=0 会把旧历史当成本次回答。
- 示例配置必须进入自动化 validate 测试，否则字段别名会和 loader 漂移。
- fake/offline smoke 应验证结构，real smoke 不能默认触发。

## 踩坑

- Slash 直调也会生成 turn.completed；TUI 不更新 cursor 时下一条聊天会立即返回。
- 示例用 `timeout:"120s"`，代码最初只接受 `timeout_ms`。
- smoke 用例有 input 但 CLI 只传 account/capability 时，测试覆盖不到真实请求构造。

## 可复用做法

- 用 `message_id -> turn_id` 建立请求关联。
- 每个破坏命令空间的操作后同步服务端 watermark。
- 配置样例、loader、CLI、fake server 放在同一回归测试里。

## 结论

✅ 推荐。
