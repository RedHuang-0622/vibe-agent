# 最终审查报告

## 变更概览

本轮在既有实现上补齐 Actor 终态原子化、请求/turn 关联、TUI cursor、
Account 配置/审计/smoke、binding timeout、checkpoint digest、Rerank strict、
进程 health/generation、协议关联、优雅停止，以及 DeepSeek Flash
thinking/effort/API key 装配。详细文件与 API 见
[code-changes.md](code-changes.md)。

## 审查结论

| 维度 | 状态 | 备注 |
|---|---|---|
| 正确性 | ✅ | `-race -count=3`、全量测试、四 exe UAT 通过；Actor 与 queue 的先后关系已用回归测试固定 |
| 可读性 | ✅ | `gofmt` 无差异；最大非测试 Go 文件 `hub/manager.go` 477 行，低于 500 行优先目标 |
| 架构 | ✅ | Sessiond 仍是唯一事件写者；Hub 不写 Session 事件；contracts 无 internal 依赖；Sessiond 无 Hub/account adapter 依赖 |
| 安全性 | ✅ | 账户 URL/credential ref 校验、loopback+token、constant-time token、credential lease、read_docs traversal、UAT secret 扫描 |
| 性能 | ✅ | Tool timing 使用 monotonic 结果；0ms 样本保留；查询/统计为本地 JSONL 重建，当前单机单用户范围可接受 |
| 语言专项 | ✅ | 无 `return nil, nil`、无业务包级可变全局、无 hardcoded secret；panic handler 被 Tool Runtime 转为稳定错误 |

## 发现的问题

### 🚨 严重

无。

### ⚠️ 警告

1. `hub`、`sessiond`、`operator` 的 Go statement coverage 仍低，主要行为由
   Windows UAT 覆盖。若后续提升故障矩阵，应补充可注入 process launcher、
   Hub HTTP fixture 和 TUI session/account 表驱动测试。
2. Sessiond 使用 loopback TCP + 随机 token + generation，而非 Windows named
   pipe。已作为首版边界记录在 README，transport 可通过 `sessionruntime` 后续替换。
3. GitHub hosted CI 尚未运行：仓库无 remote，本轮遵守用户要求未 commit/push。
   本地等价命令和 Windows UAT 已通过。
4. 真实 API smoke 未执行：实现与显式 opt-in CLI 已就绪，需要操作员提供真实
   account JSONL 与 key；fake/offline smoke 已通过。

### 💡 建议

- 将 statement coverage 门禁按核心领域逐步提升，而不是一次要求四入口 main 覆盖。
- 后续可增加 transport 替换到 Windows named pipe 的契约测试。
- 为 20 Session 压力增加 Go 进程级自动化，并保留 Windows UAT 作为最终系统验证。

## ✅ 亮点

- `turn.completed` 提交与活动槽释放收敛进 Actor，消除非确定性调度窗口。
- 聊天等待按 `message_id -> turn_id` 关联，不再使用“任意下一个 turn.completed”。
- 文件驱动 smoke、fake server 与真实 API 严格分离，secret 不进入配置/事件/报告。
- DeepSeek chat/tool_call 可精简配置并自动落到 `deepseek-flash`；Gateway 请求
  与官方 curl 入参一致，UAT fake provider 会拒绝缺失 thinking/effort 的请求。
- 删除投影后 trace/stats 重建与压缩 checkpoint digest 校验均有测试。

## 最终判断

✅ 代码与本地验收通过，可进入提交/推送。

剩余动作不由代码修复决定：用户确认提交后运行 hosted CI；需要真实 API 时显式
提供凭据并执行 opt-in smoke。
