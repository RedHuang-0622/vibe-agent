# 前置审查报告

## 需求摘要

在现有 `hub`、`sessiond`、`startup`、`tui` 骨架之上，实现可运行的本地 Agent CLI 运行时，覆盖单写者 JSONL 事件账本、会话 Actor/队列、进程生命周期与重新 attach、统一工具运行时、默认工具、Account 能力路由、混合 memory、ReAct、压缩、trace/stats、启动项和 TUI，并保持 Hub Account 读写、Sessiond Account 只读的既定边界。

## 当前基线

- 四个 `cmd/*` 入口仅输出 skeleton 文本，没有参数解析、生命周期或依赖装配。
- `pkg/contracts` 已有 Account、credential、tool、knowledge、session v1 基线，但 Session 控制/事件/查询 wire DTO 尚不足以支撑进程通信。
- Account 领域已有聚合校验，Hub/Sessiond method allowlist 已有单测；没有 JSONL Repository、应用服务、credential lease、provider adapter。
- `eventledger`、`conversation`、`contextmgr`、`tooling`、`knowledge`、`llm`、`sessionruntime`、`observability`、`operator`、`platform`、`bootstrap` 均没有业务实现。
- CI 已有 baseline、领域 matrix、Hub/Sessiond 独立门禁、contracts 依赖检查、README 门禁、vet/race 和 Windows artifact。
- 所有仓库文件仍未跟踪；没有既有 commit 可作自动回滚点，不得覆盖无关用户改动。

## 影响文件清单

| 文件路径 | 修改类型 | 具体位置 | 修改原因 |
|---|---|---|---|
| `pkg/contracts/session/v1/*` | 修改/新增 | 现有 `types.go` 与新增控制、事件、查询 DTO | Hub/TUI/Sessiond 稳定跨进程协议 |
| `pkg/contracts/tool/v1/types.go` | 修改 | Tool metadata、direct request、result、quota 字段 | 统一模型调用与 slash 直调 |
| `pkg/contracts/knowledge/v1/types.go` | 修改 | retrieval metadata、warning、版本/fingerprint | 可解释 strict/degraded 检索 |
| `pkg/contracts/account/v1/*` | 修改/新增 | 保持 v1 字段语义，补 JSONL/审计所需且不泄漏 secret 的 DTO | Account 管理应用服务与双端 fixture |
| `internal/eventledger/*` | 新增实现 | envelope、append、replay、尾损坏检测 | `messages.jsonl` 唯一真相源与 seq |
| `internal/conversation/*` | 新增实现 | Actor、pending queue、turn、ReAct 决策 | 单会话串行状态机 |
| `internal/tooling/*` | 新增实现 | Registry、Runtime、schema 子集、timer、ToolError | 统一工具管道 |
| `tools/*` | 新增 | calculator/search/read_docs/todo/weather/memory | 八个默认工具 |
| `internal/accounts/app/*` | 修改/新增 | AccountAdmin/Reader 应用服务、revision、审计 | Hub 读写 |
| `internal/accounts/adapters/*` | 新增实现 | JSONL store、credential ref、fake smoke | Hub 持久化与离线验证 |
| `internal/llm/*` | 新增实现 | chat/embedding/rerank/search adapter、模型网关 | ReAct 与多能力账户 |
| `internal/knowledge/*` | 新增实现 | tokenizer、BM25、dense、RRF、reranker、store | 自研混合召回 |
| `internal/contextmgr/*` | 新增实现 | 安全红线、上下文构建、压缩服务 | 可恢复上下文 |
| `internal/observability/*` | 新增实现 | trace、统计、P50/P95、重建 | 投影可删除重建 |
| `internal/sessionruntime/*` | 新增实现 | framed transport、Hub router、sessiond client/server | Hub/Sessiond IPC 和 reattach |
| `internal/platform/*` | 新增实现 | session lock、进程、启动项 | Windows 优先的 OS 边界 |
| `internal/hub/*` | 修改/新增 | Hub 控制面、进程表、Account/Knowledge/查询 API | 控制面职责 |
| `internal/sessiond/*` | 修改/新增 | 单会话 server、恢复、命令路由 | 数据面职责 |
| `internal/operator/*` | 新增实现 | CLI/TUI client 与 slash 解析 | 用户交互 |
| `internal/bootstrap/*` | 新增实现 | 显式装配四个二进制 | Composition Root |
| `cmd/{hub,sessiond,startup,tui}/main.go` | 修改 | 参数、退出码、生命周期 | 可运行入口 |
| 各受影响 package `README.md` | 修改/新增 | Role/API/Boundaries/Verification | 仓库门禁 |
| `.github/workflows/ci.yml` | 修改 | 新增集成/UAT/故障门禁 | required checks |
| `docs/agent-runtime/{code-changes,06,07,README}.md` | 修改 | 里程碑、追溯、命令 | 交付文档 |

## 依赖分析

- `cmd/*` 只依赖 `bootstrap`、`operator` 和公共契约；不直接实现业务逻辑。
- Hub 依赖 Account Admin、KnowledgeService、ProcessManager、Session registry、observability query；不得依赖或写入 Event Ledger。
- Sessiond 依赖 Account Reader、Event Ledger 单写者、Conversation Actor、Tooling Runtime、LLM Gateway、ContextManager、Observability projector；不得依赖 Hub 实现或 Account JSONL adapter。
- Tool 实现只实现 Tooling handler 端口；不写事件账本、不拥有 Registry。
- Knowledge 写入和索引由 Hub 托管；memory tools 通过最小 KnowledgeClient 调用 Hub。
- `pkg/contracts` 只依赖标准库；领域类型通过手写 mapper 转换，不泄漏 Repository、文件路径或 secret。
- `platform` 通过小接口承接文件锁、进程和启动项；领域代码不出现 `runtime.GOOS` 分支。

## 循环依赖检查

- [x] Hub 不进入 Event Ledger/Conversation 依赖链。
- [x] Sessiond 不进入 Hub 或 Account adapter 依赖链。
- [x] Tooling 依赖消费方端口，不反向依赖 Conversation。
- [x] Knowledge 不拥有 tool events；Sessiond 仍写工具事件。
- [x] `pkg/contracts` 不依赖 `internal/*`。
- [x] CI 增加 `go list -deps` 静态门禁，防止后续回归。

## 风险预估

- 进程生命周期与 Windows 本地端点：高概率，高严重度。需要会话锁、generation、endpoint 元数据和 kill/restart 集成测试。
- JSONL 单写者与权威文件存在边界：中概率，高严重度。写锁必须覆盖 seq 分配、append、flush、sync；不得由 Hub 或工具持写句柄。
- ReAct/Actor 的异步回调绕过串行化：中概率，高严重度。模型/工具调用完成后只能投递 Actor 命令。
- 账户 secret 泄漏：中概率，高严重度。只保存 CredentialRef，lease 短时有效且测试扫描事件/输出。
- BM25/CJK 与确定性排名：中概率，中严重度。手算对拍、稳定 tie-break 和原子快照。
- 压缩切断 pending tool 或吞队列消息：中概率，高严重度。红线解析器先识别 accepted-not-consumed 与未配对 call。
- 投影误成第二事实源：低概率，高严重度。所有查询由 JSONL rebuild，测试删除投影后结果一致。
- 四个入口一次接入过多能力：高概率，中严重度。按接口优先与领域门禁逐步集成，保持每阶段可构建。

## 建议方案

保留既定“Hub 控制面 + 每 Session 一个 sessiond + sessiond 内 Actor”架构。先扩展公共 wire DTO，再按 M0/M1/M2/M3 实现可验证的账本、IPC 和 Actor；随后以同一 Tool Runtime 接入默认工具与 slash，以 capability-first 账户解析接入 fake provider；再实现 Knowledge/Hybrid、ReAct、压缩、投影和前端。内部本地 IPC 使用长度前缀 JSON，随机 token、loopback/受限端点元数据和 generation 校验；所有可选外部 API 默认走 fake，真实 smoke 必须显式开启。

## Gate 1 记录

用户已给出完整目标、固定架构和“除非阻塞否则持续推进”的执行授权，且明确要求本轮继续。因此不重复停留等待目标确认；后续若出现会改变公共语义、引入外部依赖或产生付费 API 调用的决策，再单独请求确认。

## 续审记录（2026-11-04）

### 当前基线核对

- `go build ./...`、`go test -count=1 ./...`、`go vet ./...` 和 package README 门禁通过。
- Windows 四 exe 已构建，`scripts/uat.ps1` 的 A/B 会话、崩溃隔离、Hub reattach、ReAct、slash、memory、投影重建、startup/TUI 主链路通过。
- `go test -race -count=3 ./...` 首轮在 `TestActorCompression` 暴露终态提交与 Actor 活动槽释放之间的时序窗口。
- 当前实现已不再是纯骨架；本轮只补正确性、恢复性、交互游标、配置兼容和文档真实性，不回退已通过能力。

### 新增影响点

| 文件路径 | 具体位置 | 修改原因 |
|---|---|---|
| `internal/conversation/actor.go` | `handle(appendRequest)`、后台命令投递 | `turn.completed` 落盘后原子释放活动 turn，避免紧随压缩/下一条消息被误拒 |
| `internal/operator/{cli,tui}.go` | submit 后的等待、session/use/slash 游标 | 按 `message_id -> turn_id` 等待本次 turn，避免排队或旧 `turn.completed` 导致提前返回 |
| `internal/accounts/**`、`config/llm-accounts.example.jsonl` | JSONL loader、domain validation、audit context | 支持模板中的 `timeout: "120s"`，补齐 URL/credential ref 校验、actor/request 审计 |
| `internal/llm/gateway.go`、`tools/{search,weather}` | provider 请求超时 | 将 capability binding 的 `timeout_ms` 真正应用到调用 |
| `internal/observability/stats.go` | timing 样本判定 | 0ms 的合法样本不能被当成缺失 |
| `internal/contextmgr`、`internal/conversation/compression.go` | checkpoint 校验、安全边界 | 摘要来源不一致时回退；禁止在 assistant 消息后、turn 完成前切断 |
| `internal/knowledge`、`tools/todo` | update/persist 失败回滚 | 失败时不留下未持久化的“成功”状态，rerank 漏项按 schema 错误处理 |
| `internal/{hub,sessiond,sessionruntime}` | health、generation、request/response 校验、优雅停止 | 加强 reattach 身份校验与进程停止可靠性 |

### 续审风险

- 上述修复跨越 Actor、进程、账户与投影，必须逐域回归，不能只看 UAT 成功。
- TUI 游标和队列等待问题需要新增 operator 测试，否则现有 UAT 不会覆盖“旧 turn.completed”场景。
- 公共契约只允许新增向后兼容字段；现有 v1 字段语义保持不变。
