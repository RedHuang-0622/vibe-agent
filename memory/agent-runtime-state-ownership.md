---
name: agent-runtime-state-ownership
description: Lessons from implementing the sessiond event ledger, Actor terminal lifecycle and Hub reattach validation.
metadata:
  type: project
  tags: [go, actor, event-ledger, process-lifecycle, concurrency]
---

## 场景

在 vibe_agent 中实现每 Session 独立 Sessiond、单 Actor、JSONL 真相源和 Hub
崩溃后 reattach。

## 方案

Actor 是所有会话状态变更入口；`turn.completed` 由 Append 命令成功提交后，
在同一 Actor 处理点释放活动 turn。Hub reattach 同时校验 metadata 与 health
的 session_id、pid、generation、protocol。

## 关键经验

- 看到 `turn.completed` 文件行不等于 Actor 已完成内存状态迁移；活动槽释放要和
  终态事件提交放在同一串行边界。
- 后台模型/工具回调只能投递 Actor 命令，且投递需要同时监听 Actor done。
- session lock 只保证单写者；reattach 还需要 generation 和健康身份校验。

## 踩坑

- 仅等待 ledger 中出现终态后立即调用压缩，会命中旧的活动 turn 检查。
- Windows 上测试失败后未关闭 Actor/Writer 会导致临时目录文件仍被占用。
- Hub stop 若发完 shutdown 立刻 Kill，会绕过 Sessiond 的刷盘和收尾。

## 可复用做法

- Terminal Append -> 释放 active -> wake queue，在同一 Actor 命令内完成。
- Process wait channel 优先等待自然退出，超时才 Kill。
- 每个高风险修复配一个“紧邻终态”的回归用例。

## 结论

✅ 推荐。
