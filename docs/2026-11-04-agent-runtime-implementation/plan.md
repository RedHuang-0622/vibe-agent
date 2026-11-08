# 实现方案

## 设计目标

在不引入 Agent/工作流/ORM/RAG 框架的前提下，把四个 skeleton 入口推进为可运行系统。实现必须让 `sessiond` 成为单一 Session 的唯一事件写者，Hub 只管理进程和跨会话基础设施；账户、工具、模型、知识库和投影均通过显式注入的端口组合。

## 设计模式选择

| 模式 | Go 实现 | 应用位置 | 理由 |
|---|---|---|---|
| Command | 类型化 command + reply channel | Conversation Actor、控制面 | 所有会话变更回到单一串行入口 |
| Strategy | 消费方 interface + 构造注入 | LLM、Embedding、Rerank、Tokenizer、Summarizer | fake 与真实 adapter 可替换 |
| Adapter | 包内 struct 封装协议/文件/OS | HTTP provider、JSONL、IPC、platform | 外部格式不进入领域 |
| Factory | `NewXxx(config, deps)` | bootstrap、Registry、Clients | 禁止 singleton，便于测试 |
| Chain of Responsibility | 固定阶段函数链 | Tool Runtime | parse/validate/authorize/execute/timing 分层 |
| Repository | 最小读写端口 | Account、Memory、Todo、Event Ledger | 领域不依赖 JSONL/HTTP |
| Projector | 纯函数从事件构建 | trace/stats/context | 可从真相源删除重建 |

## 方案对比

### 方案 A：标准库 framed JSON + 分层纵向切片（推荐）

Hub/TUI 使用 loopback HTTP + 随机 token；Hub/Sessiond 使用长度前缀 JSON 帧，Sessiond 仅绑定 loopback 随机端口，并把 endpoint、pid、generation 和短期 token 写入受保护元数据。先完成 Event Ledger/Actor 垂直切片，再逐层接入工具、账户、知识库、压缩和投影。所有依赖由 bootstrap 创建。

| 维度 | 定性评价 |
|---|---|
| 耦合度 | 低。跨进程只依赖 v1 DTO；领域通过端口连接 |
| 内聚性 | 高。每个领域拥有自己的模型/服务，写入权边界明确 |
| 可测试性 | 高。账本、Actor、工具、检索可单测；IPC 可进程测试 |
| 实现成本 | 中高。需要手写帧协议和窗口刷新 |
| 改动面 | 涉及全部骨架包，但可按子目标独立回滚 |
| 可回滚性 | 高。每切片先接口、再 adapter、再装配 |

### 方案 B：`net/rpc` / 第三方 RPC 框架

使用 Go `net/rpc` 或外部 RPC 库承载 Hub/Sessiond/TUI 通信。

| 维度 | 定性评价 |
|---|---|
| 耦合度 | 中。类型与 transport 更紧密，默认编码不满足 JSON 契约 |
| 内聚性 | 中。server 注册容易演变为中央方法与全局状态 |
| 可测试性 | 中。标准编码器不如显式 DTO 易做兼容 fixture |
| 实现成本 | 低到中。但需要绕开默认 gob 或框架约束 |
| 改动面 | 中。领域仍要另行实现 |
| 可回滚性 | 中。更换 transport 影响全部调用方 |

### 方案 C：进程内共享文件 + 轮询

Hub 和 Sessiond 仅通过 JSONL 与状态文件交换命令/结果。

| 维度 | 定性评价 |
|---|---|
| 耦合度 | 低。没有直接网络/管道协议 |
| 内聚性 | 低。命令信封、ack、超时和订阅都会侵入账本语义 |
| 可测试性 | 中。时序和轮询使测试不稳定 |
| 实现成本 | 表面低，实际错误恢复成本高 |
| 改动面 | 大。可能迫使业务事件与传输事件混写 |
| 可回滚性 | 低。会污染真相源 |

## 推荐：方案 A

**理由**：满足既定进程边界，v1 DTO 可独立做 wire fixture，Sessiond 的本地端点与单写者所有权清晰；标准库即可完成。Sessiond endpoint 使用 loopback 随机端口而非公共监听，强制随机 bearer token、帧上限和 generation 校验。该实现不引入外部依赖。

**最大风险**：Windows 上本地 loopback 的安全约束弱于命名管道；因此 token 使用加密随机数、元数据文件仅当前用户可读、进程拒绝旧 generation，并在文档中明确首版边界。后续可在不改领域接口的前提下替换 named-pipe transport。

## 循环依赖检查

```text
cmd -> bootstrap/operator -> hub|sessiond -> domain/app ports
sessionruntime -> contracts + conversation command ports
conversation -> eventledger/tooling/llm/contextmgr ports
knowledge -> BM25/vector ports + account Reader
observability -> eventledger read model
accounts/app -> accounts/domain
accounts/adapters -> accounts/app + domain
```

- Hub 不 import Conversation/EventLedger。
- Sessiond 不 import Hub/Account adapters。
- Tools 不 import Conversation 或 EventLedger。
- Contracts 不 import internal。

## 核心接口

```go
type EventAppender interface {
    Append(ctx context.Context, draft Draft) (Event, error)
}

type EventReader interface {
    ReadRange(ctx context.Context, after int64, limit int) ([]Event, error)
    LastCommitted(ctx context.Context) (int64, error)
}

type ToolHandler interface {
    Invoke(ctx context.Context, call Call) (Result, error)
}

type ModelClient interface {
    Complete(ctx context.Context, request Request) (Response, error)
}

type KnowledgeClient interface {
    Write(ctx context.Context, request WriteRequest) (RecordRef, error)
    Search(ctx context.Context, request SearchRequest) (SearchResponse, error)
    Update(ctx context.Context, request UpdateRequest) (RecordRef, error)
}

type LeaseBroker interface {
    Issue(ctx context.Context, account accountv1.AccountRef, capability accountv1.Capability) (string, error)
    Redeem(ctx context.Context, leaseID, invocationID string) ([]byte, error)
}

type SessionCommand interface {
    apply(*ActorState) error
}
```

接口由消费方定义；跨进程稳定 DTO 放 `pkg/contracts/*/v1`。不把 Repository、文件格式或 provider 类型放进契约。

## 实现步骤

| # | 子目标 | 主要文件 | 验证 |
|---|---|---|---|
| G1 | 公共 wire DTO | `pkg/contracts/{session,tool,knowledge}/v1` | JSON fixture、NoInternal import |
| G2 | Event Ledger | `internal/eventledger` | 连续 seq、A/B 隔离、尾半行、重放 |
| G3 | Actor/队列/ReAct | `internal/conversation` | 队列、cancel、步数、恢复 |
| G4 | Tool Registry/八工具 | `internal/tooling`、`tools/*` | direct/model、schema、quota、timing |
| G5 | Account/LLM | `internal/accounts`、`internal/llm` | revision、lease、fake provider |
| G6 | Knowledge | `internal/knowledge` | BM25 手算、CJK、RRF、strict/degraded |
| G7 | Context/压缩 | `internal/contextmgr` | 红线、pending tool、重启 |
| G8 | Trace/stats | `internal/observability` | 重建、P50/P95、in-flight |
| G9 | IPC/进程 | `internal/{sessionruntime,platform,hub,sessiond}` | spawn、kill、reattach、generation |
| G10 | startup/TUI/CLI | `internal/operator`、`cmd/*` | 命令、断线补拉、启动项 |
| G11 | CI/文档/UAT | `.github`、`docs`、README | 全量 required checks |

## 测试策略

- 领域单元：表驱动、临时目录、fake clock/HTTP/server。
- 契约：双端 marshal/unmarshal fixture，禁止 internal import。
- 集成：event ledger + Actor + tools + fake model；Account IPC 权限；Knowledge hybrid。
- 进程：创建 A/B sessiond；杀 A 后 B 可继续；重启 Hub reattach A。
- 故障：JSONL 尾半行、调用中断、压缩失败、lease 过期、投影删除。
- 验收：构建四 exe，使用 CLI/TUI 命令完成 chat、slash、compress、trace/stats 和账户视图。
- 并发：`go test -race -count=3 ./...`。

## 回滚方案

当前仓库没有 commit，本轮不自动 commit。按领域切片记录 `docs/agent-runtime/code-changes.md`：任一领域出现不可恢复回归时可移除该领域的 adapter/装配入口，保留公共接口和其它已通过切片。真实 API smoke 和本地数据均被 `.gitignore` 排除。最终向用户提供建议 Conventional Commit message，不自动提交或推送。

## Gate 2 记录

目标已固定 Hub + 独立 Sessiond + Actor 架构，用户要求持续推进。方案 A 是唯一完全贴合该约束且不新增依赖的方案；记录此决策并直接进入编码。若实施中必须改用第三方依赖、改变公共契约语义或调用付费 API，将暂停请求用户决策。

## 续审实施方案（继续采用方案 A）

### 执行切片

| # | 切片 | 关键改动 | 验收 |
|---|---|---|---|
| C1 | Actor 终态原子化 | 处理 `turn.completed` 时在同一 Actor 命令内释放活动 turn；后台回调使用可关闭的命令投递 | 观察到终态后可立即压缩；无后台 goroutine 阻塞 |
| C2 | 请求关联与 TUI 游标 | chat 按 `message_id` 关联 consumed/turn；session 切换、slash、cancel、compress 后同步 watermark | 旧 `turn.completed` 不会提前结束新消息 |
| C3 | Account/LLM 配置正确性 | 支持 `timeout` 与 `timeout_ms`；校验 URL、credential ref、binding capability；审计 actor/request ID；binding 超时生效 | 仓库示例可直接 validate/import；fake provider 回归 |
| C4 | 检索与持久化一致性 | metadata copy、rerank schema 校验、BM25 checkpoint digest、todo 写失败回滚 | 失败不污染内存/索引；strict 模式不静默降级 |
| C5 | 进程协议稳健性 | request/response id/version、health generation、metadata 校验、优雅停止 | Hub reattach 和 stop 结果可重复 |
| C6 | 文档与门禁 | package README、code-changes、里程碑、V 模型、UAT 扩展 | build/test/vet/race/pre-commit/四 exe 全通过 |

### 测试策略

- 单元：Actor 终态后立即压缩、operator 请求关联、0ms 统计样本、timeout alias、URL/credential 校验、todo 失败回滚、rerank 漏项。
- 集成：Hub/Sessiond health 与 generation 校验、Account audit、LLM binding timeout、Windows UAT 扩展 TUI 换会话后聊天。
- 回归：原有 build/test/vet、package README、contracts 与 sessiond 依赖方向门禁。

### 回滚边界

每个切片独立更新测试与 package README；若某切片失败，可仅回退该领域的实现，不影响已通过的事件账本和进程拓扑。仓库仍未提交，最终只提供建议 commit message，不自动 commit/push。
