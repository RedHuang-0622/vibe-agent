# vibe_agent

一个不依赖 Agent 框架、以 Go 实现的 CLI Agent 运行时基线。

当前仓库已实现**可运行的本地 Agent CLI 运行时**：四个 Windows 入口、独立 Sessiond 进程、Actor/ReAct、统一工具运行时、多能力账户、BM25 混合召回、压缩、trace/stats、启动项与 TUI 均已有实现和自动化验证。当前能力覆盖：

- 工具注册、校验、执行、超时与失败分类
- `/<toolname> <req>` 直调和模型原生工具调用
- 工具结构化异常（含额度不足）与完整耗时记录
- 会话内 ReAct Loop
- 每会话独立进程与 Actor 邮箱
- 会话内单调递增 `seq` 与 `messages.jsonl` 事件真相源
- `session.snapshot/watch/history` 可恢复观察面与 TUI tail+older 缓存
- 每会话独立运维 `todo_board`：模型/斜杠 CRUD、状态流转、版本与幂等
- 基于事件 `seq` 红线的自动/手动上下文压缩
- 工具调用链路追踪与简单统计
- 多 LLM 账户、模型路由和凭据引用
- DeepSeek Flash 默认装配：`deepseek-flash`、thinking、reasoning effort 和 `DEEPSEEK_API_KEY`
- 账户级 chat/tool_call/embedding/rerank/search capability
- memory 知识库的 BM25 + embedding + RRF + reranker 混合召回
- DDD 限界上下文、模块接口、V 模型验证与里程碑看板
- Hub 读写、Sessiond 只读的 Account 公共契约
- commit 自动 build/test 基线与领域分拆 CI

## 运行方式

### 使用 Windows 发布包

发布包内双击 `启动TUI.bat`。launcher 会：

1. 使用包内 `data/` 作为 `VIBE_AGENT_HOME`；
2. 未设置 `DEEPSEEK_API_KEY` 时提示输入，key 不落盘；
3. 后台启动 hub；
4. 首次自动导入 `config/llm-accounts.local.jsonl`；
5. 启动 `bin/tui.exe`。

### 从源码运行

先构建四个入口：

```powershell
go build -o bin/hub.exe ./cmd/hub
go build -o bin/sessiond.exe ./cmd/sessiond
go build -o bin/startup.exe ./cmd/startup
go build -o bin/tui.exe ./cmd/tui
```

设置运行目录和凭据引用后启动：

```powershell
$env:VIBE_AGENT_HOME = "G:\Project\vibe_agent\data"
$env:DEEPSEEK_API_KEY = "<your-key>"

bin\startup.exe run
bin\hub.exe llm account add --file config\llm-accounts.local.jsonl
bin\tui.exe
```

直接前台运行 Hub 可用于调试：

```powershell
bin\hub.exe serve
```

常用环境变量：

| 变量 | 作用 |
|---|---|
| `VIBE_AGENT_HOME` | Hub/session/memory/account 数据根目录 |
| `VIBE_HUB_ADDR` | Hub loopback 地址；`127.0.0.1:0` 表示随机端口 |
| `VIBE_HUB_PATH` | startup 查找的 hub 可执行文件 |
| `VIBE_SESSIOND_PATH` | Hub 启动会话时使用的 sessiond 可执行文件 |
| `DEEPSEEK_API_KEY` | DeepSeek chat/tool_calls 的短期 credential lease 来源 |

## 系统设计

采用“`hub.exe` + 每会话一个独立 `sessiond.exe` + 启动项 + TUI 前端”：

```text
tui.exe -> hub.exe -> sessiond.exe (Session A) -> Actor A -> messages.jsonl
                      sessiond.exe (Session B) -> Actor B -> messages.jsonl

startup.exe -> 幂等拉起 hub.exe
```

会话隔离边界是 OS 进程，不是协程。`sessiond.exe` 每个只服务一个会话；会话内 Actor 是单线程命令调度语义，负责串行化状态变更和待发送队列，不承担跨会话隔离。每个 `sessiond.exe` 是该会话唯一的 `seq` 发放者和 `messages.jsonl` 写者。`messages.jsonl` 是会话事实的唯一真相源，压缩摘要、trace 和统计均可从它重建。

### 控制面与数据面

| 组件 | 负责 | 不负责 |
|---|---|---|
| `tui.exe` | 输入、展示、会话切换、订阅/补拉 Hub 视图 | 不直接连接 sessiond，不读 JSONL |
| `hub.exe` | 会话路由、sessiond 生命周期、Account/Knowledge、跨会话查询 | 不发放会话 `seq`，不写 Session 事件 |
| `sessiond.exe` | 单 Session Actor、ReAct、工具、压缩、`messages.jsonl` 唯一写者 | 不服务其他 Session |
| `startup.exe` | 幂等拉起 Hub、安装/移除当前用户启动项 | 不启动 sessiond，不恢复会话 |

### 状态与事件流

```text
user input
  -> tui.exe
  -> hub.exe route
  -> sessiond Actor
  -> Event Ledger (seq + fsync)
  -> projection / watch / history / snapshot
  -> tui.exe view cache
```

- 事件顺序只由 `seq` 决定，时间戳只用于审计。
- 流式增量不是领域事实；最终模型消息、工具结果和 turn 终态写入 JSONL 后才成为权威状态。
- TUI 只保留 tail/older 展示窗口；`session.snapshot/watch/history` 出现缺口时重新快照。
- Hub 的进程状态表是控制面缓存，Session 事实以 sessiond 重放结果和事件账本为准。
- 模型 tool_call 与 `/toolname` 直调经过同一 Registry、参数校验、Tool Runtime、事件和统计链路。
- 跨进程稳定 DTO 放在 `pkg/contracts/<domain>/v1`；公共契约不能依赖 `internal`。

## Memory 召回时机与放置方式

### 放置位置

Memory 是 Hub 托管的跨 Session KnowledgeService，不是 Session `messages.jsonl` 的一部分：

```text
<VIBE_AGENT_HOME>/
  memory/
    records.jsonl       # memory 真相数据与版本快照
    vectors.json        # 可重建的向量/份块投影
  sessions/<id>/
    messages.jsonl      # 仅当前 Session 的对话和工具事件
```

Sessiond 通过 Hub Knowledge API 访问 memory，因此 Hub 不可用时 memory 工具返回
`dependency_unavailable`，但不影响普通聊天和其他工具。Memory scope 支持
`workspace`、`user`、`global`，默认 `workspace`；当前 scope 是共享
KnowledgeService 上的过滤标签，不是 Session ID。

### 召回时机

当前实现**不会在每轮对话前自动召回 memory 并注入 system prompt**。召回由以下入口触发：

1. **模型主动工具调用**：ReAct 每轮把 `memory_search` 作为工具提供给模型；当用户询问历史决策、项目约定、以前写入的事实，或模型判断需要外部长期上下文时，由模型发起 `memory_search`。
2. **用户斜杠直调**：TUI/CLI 输入 `/memory_search <query>`，直接执行 Tool Runtime，不触发 LLM。

推荐的召回策略：

- 长任务开始时召回项目约束、既有决策和用户偏好；
- 准备重复架构/实现工作前，先查是否已有同类决策；
- 压缩后或跨会话继续任务时，召回被压缩出当前窗口的稳定事实；
- 不要求每轮都召回，也不要把完整对话历史当 query；使用短而具体的查询；
- 临时工具输出、一次性失败日志不写入长期 memory，稳定结论和已确认决策才写入。

### 一次模型召回的数据位置

```text
llm.completed(tool_calls=[memory_search])
  -> tool.requested(memory_search, invocation_mode=model)
  -> Tool Runtime
  -> Hub KnowledgeService: BM25 + dense + RRF + optional rerank
  -> tool.completed(payload.result = hits/channels/retrieval)
  -> ReAct history 追加 role=tool 结果
  -> 下一轮 llm.requested 携带该 tool message
  -> 最终 assistant.message.completed 与 turn.completed
```

具体放置规则：

- 召回结果先作为 `tool.completed.payload.result` 写入 Session 事件账本。
- 同一 ReAct turn 的下一轮模型请求把结果作为 `role=tool` 消息发送；不会改写原 user 消息。
- `/memory_search` 直调结果直接返回 TUI/CLI，不隐式调用模型。
- 后续模型 turn 重建上下文时，`contextmgr` 会把历史 `tool.completed/failed` 结果作为对话上下文的一部分；memory 命中不会被伪装成用户消息。
- Memory 命中不会自动进入每轮 system prompt，因此模型是否使用召回结果取决于是否发起工具调用。

### 检索与写入行为

默认混合召回：

```text
BM25 top 50
  + dense top 50 (同一 embedding fingerprint)
  -> RRF(k=60)
  -> optional reranker top 20
  -> limit (默认 10，最大 50)
```

- `allow_degraded=false` 为默认 strict；embedding/rerank 失败返回稳定错误。
- 显式 `allow_degraded=true` 时才允许 BM25-only 或 RRF-only，并在结果中返回 `degraded/warnings/channels`。
- `memory_write` 和 `memory_update` 同步完成分块、embedding、向量投影原子替换和记录发布后才返回 `ready`。
- 写入/更新必须有 `idempotency_key`；更新应带 `expected_version`，冲突返回 `conflict`。
- query/document 必须使用相同 embedding model、dimension 和 normalization fingerprint；不兼容向量不会混算。

## 文档导航

- [需求基线与验收标准](docs/agent-runtime/00-requirements.md)
- [方案对比与主要决策](docs/agent-runtime/01-plan.md)
- [整体架构与运行时流程](docs/agent-runtime/02-architecture.md)
- [DDD 领域模型与事件目录](docs/agent-runtime/03-domain-model.md)
- [模块设计与接口契约](docs/agent-runtime/04-module-design.md)
- [事件、seq 与上下文压缩规范](docs/agent-runtime/05-events-and-compression.md)
- [里程碑看板](docs/agent-runtime/06-milestones-kanban.md)
- [V 模型与需求追溯矩阵](docs/agent-runtime/07-v-model-traceability.md)
- [开发指南](docs/agent-runtime/08-development-guide.md)
- [账户管理领域模型与真实 API 冒烟](docs/agent-runtime/09-account-management.md)
- [工具请求、异常、耗时与混合记忆检索](docs/agent-runtime/10-tool-requests-and-hybrid-memory.md)
- [Account 公共契约与模块角色用例](docs/agent-runtime/11-account-contract-and-role-usecases.md)
- [CI、领域门禁与自动构建基线](docs/agent-runtime/12-ci-and-build-gates.md)
- [Account Provider 与 Claude Code 配置对照](docs/agent-runtime/14-account-provider-configuration.md)
- [下一会话 Goal 提示词与文档索引](docs/agent-runtime/13-goal-handoff.md)
- [仓库 Agent 工作规范](AGENTS.md)
- [账户 JSONL 示例](config/llm-accounts.example.jsonl)
- [API 冒烟用例 JSONL 示例](config/llm-smoke.example.jsonl)
- [账户配置填写说明](config/README.md)
- [Vibe Coding 历史发布](https://github.com/RedHuang-0622/vibe-agent/releases)
- [面试架构设计题回答](ans.txt)

## 默认范围

- 首版优先支持 Windows，同时保持 Linux/macOS 可构建；平台差异通过小接口隔离。
- 首版为单机、单用户运行时，支持多个并发会话。
- 可执行文件职责固定为 `hub.exe`、`sessiond.exe`、`startup.exe`、`tui.exe`。
- TUI 只连接 hub；startup 只拉起 hub；hub 只管理 sessiond；sessiond 不管理其他会话。
- 不引入 Agent 编排框架、ORM、消息队列或 Web 前端。
- Go 标准库优先；只有 SQLite、跨平台文件锁等确有必要的底层能力才作为独立适配器引入，且不能泄漏进领域层。

## 当前实现

```text
cmd/{hub,sessiond,startup,tui}   # 控制面、会话数据面、启动项、TUI
pkg/contracts/account/v1         # Account 公共读写契约
pkg/contracts/{credential,tool,knowledge,session}/v1
internal/accounts                # Account 领域、应用、JSONL/lease/smoke adapter
internal/{conversation,eventledger,contextmgr}
internal/{tooling,knowledge,llm}
internal/{hub,sessiond,sessionruntime,observability,operator,platform,bootstrap}
tools/{calculator,search,readdocs,todo,weather,memory}
```

默认本地门禁：

```powershell
go build ./...
go test -count=1 ./...
go vet ./...
git config core.hooksPath .githooks
git hook run pre-commit
```

Windows 进程/UAT：

```powershell
go build -o bin/hub.exe ./cmd/hub
go build -o bin/sessiond.exe ./cmd/sessiond
go build -o bin/startup.exe ./cmd/startup
go build -o bin/tui.exe ./cmd/tui
powershell -File scripts/uat.ps1 -BinDir bin
```

传输/会话缓存快速冒烟：

```powershell
powershell -File scripts/smoke-chain.ps1 -BinDir bin
```

Windows 可分发压缩包：

```powershell
powershell -File scripts/package-windows.ps1 -Version 0.1.0
```

产物写入 `artifacts/`，包含四个 exe、示例配置、env-based local account
配置、`启动TUI.bat`、PACKAGE-README 与 SHA256SUMS。打包器会拒绝包含
`sk-` 明文密钥。

隐藏 PTY 工具链冒烟（fake provider，默认 CI）：

```powershell
powershell -File scripts/tui-toolchain-smoke.ps1 -BinDir bin
```

真实 provider 冒烟为显式 opt-in，可使用 `DEEPSEEK_API_KEY`，或有效的
`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_MODEL`：

```powershell
powershell -File scripts/tui-real-api-smoke.ps1 -RunRealApi -BinDir bin
```

## Vibe Coding 历史导出

本地 Codex 的 `vibe_agent` 开发记录不进入源码仓库，而是作为 GitHub Release
附件发布。导出目录包括 4 个线程的：

- `transcript.md`：用户/助手消息、工具调用与工具结果的可读记录；
- `events.sanitized.jsonl`：脱敏后的原始事件记录；
- `manifest.json`：线程、源文件、截断边界、记录类型和 SHA256；
- `checksums.sha256`：导出文件完整性校验。

发布附件名称：`vibecoding-history-2026-11-08.zip`。源码仓库中的
`vibecoding_history/` 路径被 `.gitignore` 排除，避免复制大体积开发记录。

当前线程只导出首个真实 API key 用户消息之前的记录，该消息 ordinal `1536`
及其后内容全部排除；其他 3 个历史线程完整导出。所有导出均执行
`sk-`/Authorization 脱敏。

重新导出：

```powershell
python scripts/export_vibe_coding_history.py `
  --sessions-root "$env:USERPROFILE\.codex\sessions" `
  --output "G:\Project\vibe_agent\vibecoding_history" `
  --overwrite
```

## 面试架构设计题回答

根目录 [ans.txt](ans.txt) 是面试架构设计题的原始回答记录，覆盖 Context /
Performance、Memory、Task、Tool / Session Runtime 和 Agent Runtime 架构对比。

CI 见 [.github/workflows/ci.yml](.github/workflows/ci.yml)。完整实现/验证记录见
[code-changes.md](docs/agent-runtime/code-changes.md) 和
[V 模型追溯矩阵](docs/agent-runtime/07-v-model-traceability.md)。

## 已验证场景

- A/B 两个会话各自从 `seq=1` 开始，kill 单个 Sessiond 不中断另一会话，Hub 重启可重新 attach 存活 Sessiond。
- 聊天可完成“模型工具提议 -> 工具结果 -> 再次模型”的 ReAct 闭环，并有最大步数保护。
- `/calculator`、`/search`、`/memory_search` 直调与模型工具调用共享 Registry/Tool Runtime，并生成 direct/model trace。
- 自动/非自主压缩使用安全 `red_line_seq`，不切断 pending tool 或吞未消费消息。
- A/B 会话 snapshot 与 event_id 不串流；history 分页、watch timeout 和 cursor
  重连语义由单元测试与 `smoke-chain.ps1` 覆盖。
- TUI 在回合中显示 tool request/result；每会话 `/board` 读取独立 Todo Board。
- OpenAI-compatible 与 Anthropic Messages 的模型工具续轮均有请求形状和真实 PTY 链路测试。
- BM25 + dense + RRF + reranker 可返回通道排名、fingerprint、index version 与 strict/degraded 结果。
- 删除 `projections/` 后，trace/stats 可从 `messages.jsonl` 重建。
- 账户 JSONL、revision、credential lease、文件驱动 offline/fake smoke 与 Hub/Sessiond 权限隔离已覆盖。
- Windows UAT 同时验证 startup install/run/status/uninstall、TUI 会话切换、slash、trace/stats、压缩和账户视图。

首版 Sessiond 使用受限 loopback + 随机 token + generation/metadata 校验，而非 Windows named pipe；该 transport 位于 `sessionruntime` 边界，后续可替换而不改变领域接口。
