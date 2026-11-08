# 方案对比与主要决策

## 1. 设计目标

设计必须同时满足四个硬约束：

1. 会话级独立 `seq` 与可靠追加事件流。
2. 每会话独立进程，且一个会话的长调用不影响其他会话。
3. ReAct Loop 与待发送消息队列的状态变更可串行、可恢复。
4. 压缩、trace、统计可以从真相源重建，不能形成第二事实源。
5. 模型工具调用与 Slash 直调共享同一工具运行时，并具备结构化异常和耗时。
6. memory 使用自研 BM25 + embedding + rerank 的混合召回，不依赖外部 RAG 框架。

## 2. 候选方案

### 方案 A：hub.exe + 每会话独立 sessiond.exe + 会话内 Actor

`hub.exe` 作为常驻控制面，为每个活跃 Session 启动一个独立 `sessiond.exe`。每个 sessiond 暴露一个仅当前用户可访问、受 token 鉴权的本地 IPC 端点；hub 通过长度前缀 JSON 帧通信，并在自身重启后重新接管仍存活的 sessiond。hub 通过 loopback 控制面服务 CLI 和 TUI。`startup.exe` 只在需要时启动 hub，`tui.exe` 只连接 hub。sessiond 内的 Actor 拥有会话状态、待发送队列和唯一的事件写者，但 Actor 不是会话隔离边界。

**优点**

- 直接满足独立进程和崩溃隔离。
- 进程拓扑与启动项、TUI、后台会话管理自然分层。
- 单会话只有单写者和单 seq 发放点，顺序证明简单。
- 每个会话可独立重启、停止和资源限制。
- Actor 邮箱天然串行化并发输入，并能在 LLM/tool 等待期间保留新消息。
- 后续可替换 hub 或 sessiond 的部署位置，领域层不变。

**缺点**

- 比 goroutine 模型多一次 IPC 和进程生命周期管理。
- Windows/Unix 的进程终止、孤儿回收和文件锁需要平台适配。
- 调试需要同时观察 hub、sessiond 和事件流。

### 方案 B：单 daemon 内每会话一个 Actor 协程

一个 daemon 进程内为每个 Session 创建 goroutine Actor，事件账本按会话加文件锁。

**优点**

- 实现量最小，IPC 少，调试简单。
- 单进程内跨会话统计方便。

**缺点**

- 一个 goroutine 的不可恢复 panic、GC/内存问题或阻塞可能影响全部会话。
- 不满足“独立进程管理”的强隔离目标。
- daemon 崩溃时所有活跃会话一起中断。

### 方案 C：每次 CLI 调用创建单会话进程，无常驻 Hub

`vibe-agent run --session ...` 每轮或每次命令启动进程，结束后退出；会话状态完全从文件重放。

**优点**

- 进程边界最清晰，无需常驻服务。
- 崩溃恢复天然依赖文件重放。

**缺点**

- 流式会话、后台工具调用、待发送消息和 attach 体验差。
- 每个命令都重放可能昂贵。
- 难以集中管理活跃会话、订阅事件和进程清理。

## 3. 定性对比

| 维度 | A：Hub + 独立 sessiond | B：单 daemon 协程 | C：逐命令进程 |
|---|---|---|---|
| 会话隔离 | 高：OS 进程隔离 | 低：共享进程故障域 | 高 |
| seq 正确性 | 高：会话内单 Actor/单写者 | 中高：仍需每会话串行写者 | 高但频繁重放 |
| 崩溃恢复 | 高：sessiond 可定点重启 | 低：整个 daemon 重启 | 中：每轮重建 |
| 流式与交互体验 | 高 | 高 | 低 |
| 实现成本 | 中高 | 低 | 中 |
| 跨平台复杂度 | 中高 | 低 | 低 |
| 可测试性 | 高：sessiond 可独立测试 | 高 | 高 |
| 资源占用 | 每活跃会话一个进程 | 低 | 按命令波动 |
| 满足既定目标 | 完整 | 部分不满足 | 不满足长期会话 |

## 4. 推荐方案

选择**方案 A**。它为顺序、隔离、恢复和流式交互提供了最清晰的运行时所有权，并明确采用 `hub.exe`、`sessiond.exe`、`startup.exe`、`tui.exe` 四个可执行入口。

最大风险是进程/IPC 生命周期。缓解措施：

1. sessiond 使用 Windows named pipe 或 Unix domain socket 暴露单会话本地端点；stdout/stderr 只用于日志，不能作为业务连接，从而允许 hub 重启后重新接管。
2. 控制面首版仅绑定 loopback，并使用短期会话 token 和请求大小上限。
3. sessiond 启动时从 JSONL 重建最后合法 seq、Actor 队列和压缩水位。
4. hub 维护 `session_id -> pid + generation + state`；同一会话使用独占锁防止双写。
5. 所有 sessiond 信号终止都走“停止接收 -> 完成当前提交或取消 -> 刷盘 -> 退出”；超时后才强制终止。
6. Hub 进程会扫描 session 运行元数据，校验 pid、generation、session lock 和协议版本后重新 attach；不会仅凭旧进程表盲目路由。
7. 用故障注入测试补足平台差异。

## 5. 关键设计模式

| 模式 | Go 实现 | 应用位置 | 理由 |
|---|---|---|---|
| Strategy | 调用方定义 interface + 构造函数注入 | LLM、Search、Memory、TokenEstimator | 替换 provider 而不改 Loop |
| Adapter | 包内 struct 实现外部协议 | DeepSeek、Weather、文件存储、IPC | 隔离第三方协议与错误 |
| Factory | `NewXxx(cfg, deps)` | Registry、Provider、Account、Hub ProcessManager | 显式构造，禁用全局单例 |
| Command | 带类型的 Actor 命令 + 处理函数 | Session Actor、控制面 | 串行处理用户消息、取消、压缩和回调 |
| Chain of Responsibility | 有序 middleware/handler 切片 | Tool 校验、鉴权、超时、审计 | 每层职责独立 |
| Repository | 端口接口 + 文件/网络实现 | Session、Account、Memory、Todo | 领域不感知存储 |
| Strategy | 调用方 interface + DI | tokenizer、BM25、embedding、reranker、降级策略 | 保持算法/provider 可替换 |

## 6. 核心接口草图

接口属于消费方包；以下只表达形状，最终命名在编码时固化。

```go
type EventAppender interface {
    Append(ctx context.Context, draft EventDraft) (EventRef, error)
}

type EventReader interface {
    ReadRange(ctx context.Context, afterSeq int64, limit int) ([]Event, error)
}

type ModelClient interface {
    Complete(ctx context.Context, req ModelRequest) (ModelResponse, error)
}

type ToolInvoker interface {
    Invoke(ctx context.Context, call ToolCall) (ToolResult, error)
}

type ContextProvider interface {
    Build(ctx context.Context, sessionID string, upToSeq int64, budget int) (ContextView, error)
}
```

## 7. 不可妥协的架构规则

1. ReAct Loop 不直接写文件；它向 Actor 提交命令和事件草稿。
2. hub 不写事件，不决定会话内顺序。
3. 工具实现不能访问 EventStore；工具调用事件由运行时记录。
4. Projection 不能成为运行时读取权威状态的前提；真相源永远可独立重放。
5. 自动压缩与手动压缩进入同一个 `CompressionService`，差异仅在触发来源。
6. 任何按时间排序的 trace 都是错误设计；必须按 `seq` 和显式因果字段排序。
7. BM25 必须由项目自身实现；可使用库处理基础编码/HTTP，但不能调用现成 BM25/RAG 检索框架。
8. Slash 直调、模型 tool_call 和 memory 工具必须共用注册、授权、错误和 timing 管道。
