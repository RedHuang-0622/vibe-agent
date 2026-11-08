# 需求基线与验收标准

## 1. 文档状态

| 项目 | 内容 |
|---|---|
| 状态 | 实现与本地验证基线 |
| 日期 | 2026-11-04 |
| 目标语言 | Go 1.25+ |
| 交付形态 | 原生 Go CLI，无 Agent 框架 |
| 开发方法 | DDD + V 模型 |
| 规模 | 深度架构设计 |

> 2026-11-04 更新：当前仓库已实现本文 P0 需求，并通过本地单元/集成/race、
> Windows 四 exe UAT 与 package README 门禁。具体文件、API、测试和回滚信息见
> [code-changes.md](code-changes.md)；GitHub hosted CI 尚需提交并 push 后运行。

## 2. 目标

构建一个可独立运行、可恢复、可观测的本地 Agent CLI。其核心能力是：

1. 用统一注册机制接入本地、远端和带副作用工具。
2. 在单个会话内运行 ReAct Loop，并通过 Actor 串行管理尚未送入模型的消息。
3. 每个会话独立管理事件顺序；LLM、工具调用、压缩等会话事件都进入同一追加式事件账本。
4. 压缩以确定的会话事件 `seq` 作为红线，支持自动触发和外部指定触发。
5. 由真相源重建 trace，并统计工具调用次数与失败原因。
6. 管理多个 LLM 账户、模型别名、凭据引用和路由选择。
7. 支持模型原生工具调用以及 `/<toolname> <req>` 直调，二者共享工具运行时。
8. 支持 memory 的 BM25 + embedding + reranker 混合检索，并记录完整调用耗时。
9. 为跨进程领域对象提供版本化公共 contract 包，并按最小权限区分 Hub 读写与 Sessiond 只读。

## 3. 术语

| 术语 | 定义 |
|---|---|
| Session | 独立对话上下文和事件流边界。每个 Session 有自己的 `seq` 序列。 |
| Hub | `hub.exe`。会话控制面，负责会话创建、查找、启动、停止、进程表、CLI/TUI 路由和汇总查询。 |
| Sessiond | `sessiond.exe`。只承载一个 Session 的独立操作系统进程。 |
| Session Actor | Sessiond 内唯一的命令串行单元，拥有可变会话状态和待发送队列。它是调度语义，不是会话隔离边界。 |
| Event | 已发生且需要持久化的会话事实，包括用户消息、LLM 请求/响应、工具调用/结果、压缩和状态变化。 |
| Stream Delta | LLM 流式输出在 transport 中的临时分片，不是领域事件，不进入真相源。 |
| Seq | 会话内从 1 开始、按提交顺序严格递增的整数。 |
| Event ID | `eventID = sessionID + "-seq-" + seq`，例如 `sessiona-seq-1`，跨会话唯一。 |
| Truth Source | `messages.jsonl`。每行一个事件，是重放会话的唯一事实来源。 |
| Red Line | 压缩覆盖到的最后一个源事件 `seq`，记为 `red_line_seq`。 |
| Trace | 由事件之间的 `trace_id`、`causation_seq` 和 `call_id` 还原的因果调用链。 |
| Projection | 从事件流派生出的索引、统计、会话快照或有效上下文，可删除并重建。 |
| Direct Tool Request | TUI/CLI 的 `/<toolname> <req>` 操作，不经过 LLM，但经过统一 Registry、Tool Runtime、事件和统计。 |
| BM25 | 基于词频、文档长度和逆文档频率的稀疏词法检索算法，由项目自行实现。 |
| Hybrid Retrieval | BM25 词法候选与 embedding 稠密候选融合，并可按配置经过 reranker 重排。 |
| RRF | Reciprocal Rank Fusion，按各通道名次融合候选。 |
| Embedding Fingerprint | embedding 模型、维度、归一化方式和索引版本的组合标识。 |
| Public Contract | `pkg/contracts/<domain>/<version>` 下的版本化跨进程 DTO、method、enum 和错误码；不依赖内部领域或存储实现。 |

## 4. 语义约束

### 4.1 Seq 与会话隔离

- 每个 Session 的 `seq` 独立从 1 开始。
- Session A 的 `seq=1` 与 Session B 的 `seq=1` 互不冲突，事件身份必须同时包含 `session_id` 和 `seq`。
- 顺序只由 `seq` 决定；`created_at` 只用于审计和展示，不参与先后判断。
- 已提交的 `seq` 必须连续，不允许空洞或重复。
- `seq` 在单会话写者提交前一个事件时发放，提交成功后才向调用方确认。
- 发生写入失败或末尾半行损坏时，恢复从最后一条合法事件之后继续，不信任内存中的最大序号。

### 4.2 真相源

- 每个事件占 `messages.jsonl` 的一行，采用 UTF-8 JSON。
- 原始事件只追加，不原地修改；压缩、索引、快照和统计不得重写真相源。
- 所有投影都携带其消费到的 `watermark_seq`。投影落后时按事件流增量追赶，损坏时从头重建。
- 凭据、完整系统提示词中的秘密、临时流式分片不得进入事件账本。

### 4.3 会话与进程

- 一个 Session 同一时刻只有一个 sessiond 拥有写权限。
- 每个活跃 Session 对应一个独立 `sessiond.exe`；一个 sessiond 只服务一个 Session。
- 会话进程的崩溃不能使其他会话失去服务。
- 会话隔离不得用“每会话一个协程”实现；协程只能出现在单个进程内部承担 I/O 和信号处理，不能成为会话所有权边界。
- `hub.exe` 负责启动、停止、重启、发现孤儿进程和转发控制命令，不直接修改事件账本。
- Hub 重启后应能接管仍存活的 sessiond；hub 短暂崩溃不能让已持久化的事件丢失，也不应强制中断所有会话数据面。
- `startup.exe` 负责幂等拉起 hub 和注册/检查启动项；不得直接启动 sessiond。
- `tui.exe` 只通过 hub 控制面和事件订阅面工作；不得绕过 hub 访问 sessiond 或直接写事件账本。

### 4.4 Actor 与待发送队列

- 消息接收、turn 状态变更、事件追加、压缩水位更新必须经 Session Actor 串行处理。
- 模型调用或工具执行期间到达的新用户消息可以接收并持久化，但不能绕过当前 turn 的次序直接注入正在进行的请求。
- 首版支持 `queue` 和 `cancel-and-queue` 两种队列策略；默认 `queue`。
- “未发送”不是只存在于内存的私有状态。`user.message.accepted` 与后续消费事件共同构成可重放状态。

### 4.5 压缩

- 任何压缩请求必须记录确定的 `red_line_seq`。
- 自主压缩（`autonomous`）由运行时策略触发；非自主压缩（`external`）由用户 CLI、TUI 或控制命令触发。二者使用同一条应用服务路径。
- 红线必须落在安全事件边界：不得切断未完成的工具调用，不得吞掉未消费的用户消息。
- 压缩结果作为新事件追加，不删除被压缩事件；有效上下文由“最近成功压缩摘要 + 红线后的会话事件”重建。
- 压缩失败不得推进有效水位，原上下文继续可用。

### 4.6 Trace 与统计

- 每个用户 turn、LLM 请求、工具调用都处于一个 `trace_id` 下。
- 子事件用 `causation_seq` 指向直接触发事件，用 `call_id` 关联请求与结果。
- trace 不得依赖日志文本猜测链路；仅依赖 `messages.jsonl` 事件字段即可还原主链。
- 统计至少提供按 `tool_name` 和 `tool_kind` 的调用总数、成功数、失败数和失败原因计数。
- 统计是投影；删除统计文件后必须能由事件账本重建出相同结果。
- 工具调用必须记录排队、校验、执行和总耗时；失败、超时和取消同样记录。

### 4.7 工具请求与异常

- LLM 原生工具调用与 `/<toolname> <req>` 直调必须进入同一个注册表和 Tool Runtime。
- 直调不隐式触发第二次 LLM 推理，但必须形成独立 turn/trace 和完整工具事件。
- Slash 语法由 Hub/Sessiond 服务端最终校验；TUI 的本地解析不能替代授权和 schema 校验。
- 工具异常必须返回结构化 `code/message/retryable`，额度不足使用 `quota_exhausted`，不能以进程崩溃或不可解析文本表达。
- 原始 provider 错误必须脱敏；统计和重试决策只依赖稳定错误码。
- 工具耗时使用 monotonic clock 计算，wall-clock 时间只用于展示和审计。

### 4.8 Memory 混合检索

- memory record/chunk 是独立知识库事实，不写入 Session 的 `messages.jsonl`。
- 词法通道必须由项目自行实现 BM25，包括倒排索引、IDF、文档长度归一化和稳定排序。
- 稠密通道使用账户的 `embedding` binding；向量必须按 embedding fingerprint 隔离。
- 两通道默认通过 RRF 融合，融合候选可再经过账户的 `rerank` binding 重排。
- embedding/reranker 的额度、鉴权、超时和 schema 异常必须映射为稳定工具错误。
- strict 模式不得静默降级；degraded 模式必须返回 `partial_result` warning 和实际使用通道。
- BM25/向量索引必须可从 memory 真相数据和 embedding API 重建，并有可查询的 index version。

### 4.9 公共契约与角色

- 横跨 Hub、Sessiond 和 TUI/CLI 的稳定数据必须放入 `pkg/contracts/<domain>/<version>`。
- 公共契约包只能依赖标准库，不能 import `internal/*`，不能暴露领域实体、Repository 或 JSONL 文件结构。
- Hub 对 Account 具备读/写角色；Sessiond 只具备读角色，只能解析调用所需 binding 和短期 credential lease。
- 接口分离必须与 IPC method allowlist、token scope 双重一致；仅把 Go interface 收窄不构成安全边界。
- Sessiond 不得列出、创建、更新、启停、归档账户或修改默认路由。
- 账户管理写操作必须记录审计，包含 actor、action、account_id、expected_revision、result_revision 和 changed fields。

## 5. 功能需求

| ID | 优先级 | 需求 | 可测量验收标准 |
|---|---|---|---|
| R-001 | P0 | 提供统一工具注册机制 | 注册时校验名称、版本、JSON Schema、处理器、能力标签；重复名失败；`tool list` 可列出元数据 |
| R-002 | P0 | 注册并执行 calculator | 确定性算术可执行；非法表达式返回稳定失败码；无外部网络依赖 |
| R-003 | P0 | 注册并执行 search | 通过 DeepSeek 搜索适配器执行；支持 query/limit；网络、限流、鉴权错误被标准化 |
| R-004 | P0 | 注册并执行 read_docs | 只能读取允许根目录内文件；支持按行或片段读取；越界路径被拒绝 |
| R-005 | P0 | 注册并执行 todo | 支持 create/list/update/complete；写操作有幂等键；结果可恢复 |
| R-006 | P0 | 注册并执行 weatherapi | 支持 location/date；超时、上游错误、空结果有稳定错误分类 |
| R-007 | P0 | 注册并执行 memory_write/search/update | 三个工具共享 Knowledge 端口；写入、更新完成后可召回；返回稳定 record/chunk ID；作用域明确 |
| R-008 | P0 | LLM 和工具领域事件都有 seq | `llm.requested`、`llm.completed`、`tool.requested`、`tool.completed` 均出现在事件流中且 seq 连续 |
| R-009 | P0 | 会话独立 seq 与 JSONL 真相源 | 两会话并发运行时均独立从 1 开始；重启后由 JSONL 重放得到同一状态 |
| R-010 | P0 | 每会话内运行 ReAct Loop | 能完成“模型决策 -> 工具调用 -> 工具结果 -> 再次模型决策”的闭环，并受最大步数限制 |
| R-011 | P0 | Actor 管理待发送消息队列 | 模型/工具执行时到达的消息被持久化，按策略在下一安全时点消费；重启后未消费消息仍在 |
| R-012 | P0 | 压缩红线绑定事件 seq | 每个压缩事件包含 `red_line_seq`、源范围与策略；恢复后有效上下文一致 |
| R-013 | P0 | 支持自主与非自主压缩 | 达到策略阈值由运行时自主触发；CLI/TUI 可指定或由系统协助选择红线后非自主触发 |
| R-014 | P0 | 工具调用链路 trace | 给定 `call_id` 或 `trace_id` 可还原父子事件、参数摘要、invocation mode、结果状态、错误、耗时和检索元数据 |
| R-015 | P0 | 工具调用统计 | 可按工具类型和 direct/model 模式统计调用次数、失败次数、失败原因及平均/P50/P95 耗时；结果可由账本重建 |
| R-016 | P0 | 不依赖 Agent 框架 | `go.mod` 无编排/工作流/Agent SDK；Loop、Actor、Registry、压缩均为项目代码 |
| R-017 | P0 | Go CLI | 提供 daemon、session、chat、tool、llm account、trace、stats 等命令 |
| R-018 | P0 | 账户管理 | 支持 add/list/show/use/test/remove/validate/smoke；支持多个 provider/model；不打印或持久化明文密钥到事件流 |
| R-019 | P0 | 固定进程拓扑 | 每次创建会话由 hub 启动一个独立 sessiond；20 个活跃会话对应 20 个 sessiond；任一 sessiond 崩溃不影响其他会话 |
| R-020 | P0 | 启动项与 TUI | startup 可安装/检查/移除当前用户登录启动项并幂等拉起 hub；TUI 可列出会话、进入聊天、查看工具状态/trace/压缩水位和账户信息 |
| R-021 | P0 | Slash 工具请求 | `/<toolname> <req>` 可直调已注册工具；先写 `user.tool_command.accepted`；未知工具、非法参数和权限错误稳定返回；直调事件带 seq/trace |
| R-022 | P0 | 工具异常返回 | 工具异常统一为结构化错误；至少覆盖额度不足、限流、鉴权、超时、schema、依赖不可用 |
| R-023 | P0 | 工具耗时记录 | 成功/失败事件包含 queue/validation/execute/total 毫秒；使用 monotonic clock；统计可重建 |
| R-024 | P0 | 自研 BM25 | 实现 tokenizer、倒排索引、IDF、BM25 公式、更新/删除和稳定排序；有手算对拍测试 |
| R-025 | P0 | 混合召回 | memory_search 默认 BM25 + dense 后 RRF 融合，并可配置 reranker；返回各通道排名、分数、版本和降级信息 |
| R-026 | P0 | Embedding/Reranker 账户 | 账户支持 `embedding` 与 `rerank` capability binding、模型、维度、endpoint、凭据引用、配额、超时和冒烟配置 |
| R-027 | P0 | 公共 Account Contract 与角色隔离 | `pkg/contracts/account/v1` 可被 Hub/Sessiond 消费；Hub 可读写，Sessiond 仅可读解析；Sessiond 调用管理写方法返回 `permission_denied` |
| R-028 | P0 | 可恢复的会话观察面 | `session.snapshot` 返回最新 tail/watermark；`session.watch` 用最后应用 cursor 增量补读并可超时；`session.history` 提供 `before_seq` 历史页；缺口返回 `resync_required` |
| R-029 | P0 | 每会话运维 Todo Board | 每个 session 独立持有 `todo_board.jsonl`；模型与 slash 可通过统一工具执行 list/get/create/update/delete；更新/删除使用 expected_version，所有 mutation 使用 idempotency_key；TUI `/board` 可读取 |

## 6. 非功能需求

| ID | 需求 | 验收标准 |
|---|---|---|
| NFR-001 | 会话隔离 | 任一 sessiond 崩溃不影响其他 sessiond 的服务；并发 20 会话压力测试无串流 |
| NFR-002 | 顺序正确性 | `go test -race -count=3` 无竞态；并发命令注入下 seq 无重复、无空洞 |
| NFR-003 | 可恢复性 | 在各阶段注入进程终止后，重放结果等于最后已确认状态；未消费消息不丢失 |
| NFR-004 | 可重放性 | 删除全部投影并重放 JSONL，trace、统计、有效上下文摘要一致 |
| NFR-005 | 安全边界 | read_docs 路径穿越测试失败；日志和事件不含 API key；本地控制面仅绑定 loopback 并要求 token |
| NFR-006 | 可移植性 | Windows 为首要平台，Linux/macOS 至少通过编译与核心单测 |
| NFR-007 | 可观测性 | 结构化日志包含 `session_id`、`seq`、`trace_id`、`call_id`，但不替代真相源 |
| NFR-008 | 响应性 | 一个会话的长耗时 LLM/工具调用不阻塞 CLI 操作其他会话 |
| NFR-009 | 可扩展性 | 新增工具只需提供 metadata、schema、handler 并注册；不修改 ReAct Loop |
| NFR-010 | 可维护性 | 领域层不依赖 CLI、HTTP、文件系统或进程 API；核心包无循环依赖 |
| NFR-011 | 检索一致性 | 查询/文档只比较同一 embedding fingerprint；索引更新半完成不可见；相同数据、参数和版本可复现排名 |
| NFR-012 | 真实 API 可验证性 | 提供账户 JSONL 与 smoke 用例 JSONL 驱动的 opt-in smoke，能够区分配置、凭据、鉴权、额度、限流、超时和 schema 错误 |
| NFR-013 | 最小权限 | Sessiond 仅注入 AccountReader 且 IPC token 只有 `account:read`；公共契约无 internal 依赖；写操作全部审计 |
| NFR-014 | 前端恢复一致性 | 断线后从最后展示 cursor 继续；重复事件不重复渲染，缺口不猜测内容；刷新 snapshot 不丢失已翻历史页；A/B 会话缓存不串流 |
| NFR-015 | 真实工具链路可验证性 | 隐藏 PTY 中运行独立 hub/sessiond/TUI；真实 provider 为显式 opt-in；至少验证 model tool_call -> tool result -> 后续 model/final answer，鉴权失败必须记 BLOCKED 而非 PASS |

## 7. 默认边界与假设

- 首版是单机、单用户。多用户租户、跨机器 sessiond、分布式事件复制不在范围内。
- `messages.jsonl` 是事件账本名称，但每行不限于聊天消息。
- “每会话独立进程”指每个活跃 Session 一个 OS 进程 `sessiond.exe`，不是每个 turn 启一个进程，也不是每会话一个 goroutine。
- `hub.exe`、`startup.exe`、`tui.exe` 是独立可执行文件；`tui.exe` 不内嵌会话运行逻辑。
- `todo` 与 `memory` 首版使用本地持久化适配器，与 `messages.jsonl` 分离；它们不是会话真相源。
- memory 知识库由 Hub 托管为跨会话共享服务；Sessiond 的 memory tool 通过本地 Knowledge API 调用，Session 事件仍由 Sessiond 记录。
- BM25 的默认 tokenizer 使用 Latin term + CJK unigram/bigram；中文词典分词由 Tokenizer 接口后续替换。
- embedding/reranker 账户绑定可分别使用不同 provider 和 credential ref；真实 key 通过环境变量或系统凭据存储注入。
- 真实 API smoke 默认不进入 CI，必须显式启用并读取本地账户/用例文件。
- 会话级上下文压缩与跨会话长期 memory 是不同能力，不能混为一体。
- LLM 流式 delta 作为传输层增量推送；只有归一化的请求/响应事件具有 seq。
- 搜索和天气的真实网络调用通过适配器实现；测试使用 fake server 或录制响应，不依赖外部服务稳定性。

## 8. 非目标

| 非目标 | 原因 |
|---|---|
| Web UI、移动端、多人协作 | 当前目标是 CLI 内核与可验证运行时 |
| 分布式共识、多机事件复制 | 首版使用会话级单写者即可满足顺序要求 |
| 任意 JSON Schema 的完整标准实现 | 首版只支持工具参数所需子集，并在文档中明确支持的 keyword |
| 外部向量数据库/托管 RAG 框架 | 首版用本地 BM25、向量文件和自研融合流程；不引入托管 RAG/向量数据库 |
| 自动无限压缩 | 必须有安全边界、最大连续压缩次数和失败退出条件，避免压缩递归 |
| 事件账本原地删除或重写 | 违背真相源和可审计要求 |
