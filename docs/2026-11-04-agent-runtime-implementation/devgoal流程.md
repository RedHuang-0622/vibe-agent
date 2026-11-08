# Workflow: Agent Runtime Implementation

## 元信息

- 日期: 2026-11-04
- 规模: 深度
- 需求: 从现有架构骨架完整实现可运行、可恢复、可观测的本地 Agent CLI 运行时
- 子 Skill 清单:
  - G: front-review → [front-review.md](./front-review.md)
  - O: devplan → [plan.md](./plan.md)
  - A1: code-impl → [code-changes.md](./code-changes.md)
  - A2: test-suite → [test-report.md](./test-report.md)
  - L: finish-review → [finish-review.md](./finish-review.md)

## G: Goal

> 委托: front-review | 输出: [front-review.md](./front-review.md)

### 目标拆解

| # | 子目标 | 验收标准（可测量） | 优先级 |
|---|---|---|---|
| G1 | 事件账本/seq/重放 | A/B 各从 seq=1；事件 ID 正确；尾半行恢复；重放一致 | P0 |
| G2 | Hub/Sessiond 生命周期 | 每会话独立进程；kill A 不影响 B；Hub 重启 reattach | P0 |
| G3 | Actor/队列/恢复 | 回调经 Actor；pending accepted-not-consumed 重启恢复；无双 turn | P0 |
| G4 | 工具 Registry/Runtime | 八工具可列举；direct/model 共管道；稳定错误与单调计时 | P0 |
| G5 | Account/LLM | Hub Admin、Sessiond Reader；revision/audit/lease/fake provider | P0 |
| G6 | Memory hybrid | 自研 BM25 手算、CJK、dense fingerprint、RRF、rerank、strict/degraded | P0 |
| G7 | ReAct/压缩 | 多步 tool 后模型完成；步数上限；安全红线与重启恢复 | P0 |
| G8 | Trace/stats | 仅从 JSONL 重建 trace、失败、模式和 P50/P95 | P0 |
| G9 | startup/TUI | install/run/status/uninstall；会话/chat/slash/trace/stats/compress/account | P0 |
| G10 | CI/故障/UAT | 全量 build/test/vet/race、打包、隔离与恢复场景 | P0 |

### 成功标准

- [ ] 全部 P0 需求与 Goal 完成定义关闭。
- [ ] `go build ./...`、`go test -count=1 ./...`、`go vet ./...`、`go test -race -count=3 ./...`、pre-commit 通过。
- [ ] Windows 构建四个 exe。
- [ ] 每个 Go package README 与导出 API 同步。
- [ ] 文档、V 模型、code-changes 和里程碑同步。

### 非目标

- Web/移动/多人协作、多机复制、外部 RAG/向量数据库、无限自动压缩、事件历史原地改写。

### 风险预判

进程/IPC、单写者、异步 Actor、secret 脱敏、CJK 检索、压缩边界和投影重建是本轮最主要的正确性风险。

## O: Options

> O0: dev-goal 历史经验检索 | O1-O3 委托: devplan | 输出: [plan.md](./plan.md)

### O0: 历史经验参考

| 来源 | 相关经验 | 对本次的启示 |
|---|---|---|
| `memory/` | 不存在 | 首次探索，本轮 L 阶段沉淀第一批经验 |
| `docs/*/devgoal流程.md` | 仅当前工作流 | 无历史踩坑可复用 |

### 方案摘要

| 方案 | 核心思路 | 设计模式 | 变更范围 | 主要风险 |
|---|---|---|---|---|
| A | 标准库 framed JSON + 分层纵向切片 | Command/Strategy/Adapter/Factory | 全仓库架构落地 | Windows loopback 安全弱于 named pipe |
| B | Go RPC/第三方 RPC | RPC + 领域端口 | transport 与领域 | 编码/框架耦合、全局注册倾向 |
| C | 共享文件轮询 | Repository + polling | 账本/传输混合 | 传输信封污染真相源、时序不稳定 |

### 推荐：方案 A

**推荐理由**：满足进程隔离和公共契约方向，标准库可实现，领域边界可独立验证。
**最大风险**：本地 endpoint 安全与 Windows 生命周期；以随机 token、generation、会话锁和进程故障测试缓解。

### Gate 2 记录

目标文件已固定选择 Hub + 独立 Sessiond + 会话内 Actor，并要求除非阻塞持续推进。方案 A 与既定架构一致且不新增依赖，故采用方案 A 继续执行；外部依赖、付费 API 或公共语义变更仍会先请求用户决策。

### A0: 执行调度

`tasks.json` 已生成。当前会话未授权子代理，且环境没有可用的 `soft_eng_plan` skill；采用 DAG 顺序执行并在进入依赖阶段前验证前置 domain gate。并行候选为工具/账户与 IPC，但会共享工作区且用户未要求并行，因此不启动子代理。

## A: Action（续审 2026-11-04）

### 基线结果

| 检查 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go test -count=1 ./...` | 通过 |
| `go vet ./...` | 通过 |
| package README check | 31 个 package 通过 |
| Windows UAT | 通过 A/B、崩溃、reattach、ReAct、slash、memory、投影、startup/TUI 主链路 |
| `go test -race -count=3 ./...` | 首轮失败：Actor 终态后活动槽释放存在时序窗口 |

### 本轮执行边界

继续方案 A，不引入依赖、不改变既有 v1 字段语义。按 C1-C6 切片修复并补齐回归测试；公共契约仅允许新增向后兼容字段。用户已明确要求除非阻塞否则持续推进，因此不重复设置人工 Gate。

### A2: 测试

> 委托: test-suite | 输出: [test-report.md](./test-report.md)

| 检查 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go test -count=1 ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race -count=3 ./...` | 通过 |
| 短时 fuzz（tokenizer/calculator/framed IPC） | 通过 |
| package README | 31 个通过 |
| Windows 四 exe + UAT | 通过 |
| pre-commit | 通过 |

statement coverage 在 hub/sessiond/operator 等进程与前端包仍低于 85% 建议值，
但关键行为由 Windows UAT 覆盖；该结果作为后续测试增强建议，不阻塞当前验收。

## L: Learning

> 委托: finish-review | 输出: [finish-review.md](./finish-review.md)

### 目标复核

| 子目标 | 实际结果 | 达成 |
|---|---|---|
| G1 Event Ledger/seq/重放 | 单写者、A/B seq、尾损坏、重放测试通过 | ✅ |
| G2 Hub/Sessiond | 每 Session 进程、crash isolation、reattach、stop UAT 通过 | ✅ |
| G3 Actor/队列/恢复 | terminal 原子释放、queue recovery、message→turn cursor 测试通过 | ✅ |
| G4 工具 | 8 工具、direct/model、panic/error/timing 测试通过 | ✅ |
| G5 Account/LLM | 严格 JSONL、revision、audit、lease、timeout、offline/fake smoke 通过 | ✅ |
| G6 Memory | BM25 手算、CJK、dense fingerprint、RRF、rerank strict/degraded 通过 | ✅ |
| G7 ReAct/压缩 | 多步 ReAct、步数限制、digest 与安全边界通过 | ✅ |
| G8 Trace/stats | 删除投影重建、0ms timing、P50/P95、in-flight/orphaned 通过 | ✅ |
| G9 startup/TUI | 四命令、session 切换、slash/chat、trace/stats/compress/accounts 已实现并 UAT | ✅ |
| G10 CI/故障/UAT | 本地等价门禁与 Windows UAT 通过；hosted CI 待 push | ⚠️ 外部 |

### 方案复盘

方案 A 的标准库组合与显式 DI 保持了 Hub 控制面/Sessiond 数据面边界。主要实际
成本集中在 Windows 进程生命周期、Actor 终态时序和前端 cursor，而不是公共接口。
这些风险通过进程级 UAT、race、契约 fixture 和关联等待被显式覆盖。

### 改进建议

- 下一轮优先提高 hub/sessiond/operator 的故障注入与表驱动 coverage。
- 如需要更强的本机权限边界，将 Sessiond loopback transport 替换为 named pipe。
- 提交并配置 remote 后运行 hosted required checks；真实 API smoke 由操作员显式触发。

### 经验沉淀

- [Agent runtime state ownership](../../memory/agent-runtime-state-ownership.md)
- [Operator cursor and smoke](../../memory/operator-cursor-and-smoke.md)
