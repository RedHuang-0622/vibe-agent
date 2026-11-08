# 下一会话 Goal 提示词与文档索引

> **实现状态（2026-11-04）**：本文件保留为历史 Goal 交接文本。所列运行时能力
> 已在当前工作树实现，并通过本地 build/test/vet/race、31 个 package README、
> Windows 四 exe 和 `scripts/uat.ps1`。当前实现与回滚信息以
> [code-changes.md](code-changes.md) 为准；GitHub hosted CI 需提交/push 后运行。

## 1. 当前基线

仓库：`G:\Project\vibe_agent`

当前已完成：

- 完整需求、DDD 架构、V 模型和里程碑设计。
- 四个可构建入口：`hub`、`sessiond`、`startup`、`tui`。
- 版本化公共契约包 `pkg/contracts/*/v1`。
- Account Aggregate、应用端口、Hub 读写/Sessiond 只读门禁。
- 按领域划分的 Go package 实现与测试。
- 25 个 Go package README。
- 根级 Agent 纪律 `AGENTS.md`。
- 自动 build/test CI、Hub/Sessiond 独立门禁、package README 门禁。
- 本地 pre-commit build/test 基线。

历史交接时尚未完成、现已补齐：

- Sessiond 内 Actor、ReAct Loop、待发送队列与恢复。
- `messages.jsonl` 单写者、seq 发放、尾损坏恢复。
- Hub/Sessiond framed IPC、generation 校验与重新 attach。
- calculator/search/read_docs/todo/weatherapi/memory 工具。
- Account JSONL Repository、revision、credential lease、validate/smoke。
- 自研 BM25、dense fingerprint、RRF、reranker 与 strict/degraded。
- 基于 seq 红线的自主/非自主压缩。
- trace/统计投影与删除重建。
- TUI、CLI 和 startup install/run/status/uninstall。
- Windows 端到端 UAT、崩溃隔离、20 Session 进程隔离。

当前验证基线：

```powershell
go build ./...
go test -count=1 ./...
go vet ./...
go test -race -count=3 ./...
git hook run pre-commit
powershell -File scripts/uat.ps1 -BinDir bin
```

仓库尚未创建任何 commit；所有文件仍在当前工作区。后续会话仍须先运行
`git status --short`，不得删除或覆盖已有成果。

## 2. 可直接粘贴的 Goal 提示词

```text
你是 vibe_agent 仓库的主 Agent。目标是在 G:\Project\vibe_agent 中，从当前架构与包骨架继续，完整实现可运行的本地 Agent CLI 运行时，而不是只做规划或继续堆文档。

开工前必须完整阅读：
1. AGENTS.md
2. README.md
3. docs/agent-runtime/13-goal-handoff.md
4. docs/agent-runtime/00-requirements.md
5. docs/agent-runtime/02-architecture.md
6. docs/agent-runtime/03-domain-model.md
7. docs/agent-runtime/04-module-design.md
8. docs/agent-runtime/05-events-and-compression.md
9. docs/agent-runtime/11-account-contract-and-role-usecases.md
10. docs/agent-runtime/12-ci-and-build-gates.md

使用 dev-goal 管理 Goal/Options/Action/Learning；方案实现用 devplan，编码用 code-impl，分层测试用 test-suite，完成前用 finish-review。除非出现必须由用户决策的阻塞，否则持续推进。

最终目标架构：
- hub.exe：控制面、Session 进程表、Account 读写、KnowledgeService、TUI/CLI 路由。
- sessiond.exe：每个活跃 Session 一个独立 OS 进程，拥有 Actor、ReAct Loop、待发送队列、压缩、工具、LLM 和 messages.jsonl 单写者。
- startup.exe：登录启动项、幂等拉起 Hub。
- tui.exe：只通过 Hub 使用的终端客户端。
- 不使用 Agent 编排、CLI/TUI、ORM、RAG 或工作流框架。

必须完成的功能：
1. 会话内 seq 从 1 开始、连续单调；事件 ID 为 <session_id>-seq-<seq>。
2. messages.jsonl 是唯一 Session 真相源；崩溃后能从最后合法 seq 重放。
3. Hub/Sessiond 本地 IPC、进程/会话锁、generation、崩溃隔离和 Hub 重启 reattach。
4. Session Actor 串行处理消息、LLM/tool 回调和压缩；未发送消息可持久化恢复。
5. ReAct Loop 支持带 tools 的模型请求、多步工具调用和最大步数限制。
6. /<toolname> <req> 与服务端工具调用共用 Registry/Tool Runtime。
7. ToolError 结构化返回，包括 quota_exhausted/rate_limited/auth/timeout/dependency_unavailable。
8. 每次工具成功、失败、超时、取消记录 queue/validation/execute/total monotonic 耗时。
9. 实现 calculator、search、read_docs、todo、weatherapi、memory_write/search/update。
10. Account 公共契约保持 Hub Admin 读写、Sessiond Reader 只读；credential 使用短期 lease。
11. Account JSONL、revision、多 capability binding、validate、offline fake smoke 和 opt-in real smoke。
12. memory 自研 BM25、embedding dense retrieval、RRF、reranker 和 strict/degraded 策略。
13. 基于事件 seq 的自主/非自主上下文压缩，不切断 pending tool，不吞未消费消息。
14. trace 可由真相源重建 tool 调用链、失败码、invocation_mode、duration 和 retrieval metadata。
15. stats 支持工具调用数、成功率、失败原因、avg/P50/P95 和投影重建。
16. startup.exe 支持 install/run/status/uninstall；TUI 支持会话、聊天、slash 工具、trace/stat、压缩和账户视图。
17. 完整通过 CI：baseline、contracts、accounts、领域 matrix、hub gate、sessiond gate、repo-policy、vet/race、Windows package。

执行顺序：
M0 契约/包基线 -> M1 Event Ledger/seq/重放 -> M2 Hub/Sessiond 生命周期 -> M3 Actor/队列 -> M4 Tool Registry/直调/异常/耗时 -> M5 多能力账户 -> M5B BM25/混合召回 -> M6 ReAct -> M7 压缩 -> M8 trace/stats -> M9 startup/TUI -> M10 故障/UAT。

每个子目标必须：
- 先接口、再实现、再测试；
- 更新受影响 package README；
- 运行该领域测试和目标二进制 build；
- 不破坏 Hub 写、Sessiond 只读的 Account 权限；
- 不同步跳过 V 模型需求；
- 不写明文 secret；
- 不把包级可变状态或全局 singleton 当依赖注入。

最终验收必须演示：
- A/B 两个 Session 同时从 seq=1 开始且互不干扰。
- 杀掉 A 的 sessiond，B 继续；A 可恢复。
- Hub 崩溃重启后能重新 attach 存活的 sessiond。
- 聊天触发 tool call，继续 ReAct 完成回答。
- /calculator、/search、/memory_search 直调产生 direct-tool trace。
- 模拟 quota 返回稳定错误和 reset 信息。
- 自动/手动压缩后重启，上下文一致。
- memory BM25+dense+rerank 结果可解释，strict/degraded 可测试。
- 删除投影后可由 JSONL 重建 trace 和 stats。
- Windows 构建出四个 exe，CI 所有 required gate 通过。

收尾要求：
- 更新 docs/agent-runtime/code-changes.md；
- 更新里程碑状态、V 模型追溯和 README；
- 运行 go build ./...、go test -count=1 ./...、go vet ./...、go test -race -count=3 ./...、git hook run pre-commit；
- 用户未明确要求时不要 commit/push，但提供建议 commit message；
- 只有全部 P0 需求和验收场景完成才把 Goal 标记为 complete。
```

## 3. Goal 完成定义

所有 P0 需求与 V 模型追溯项必须关闭。至少满足：

- [ ] `sessiond.exe` 是 Session 事件唯一写者。
- [ ] A/B Session 的 seq 独立、连续并可由 JSONL 重放。
- [ ] Hub/Sessiond 进程隔离、崩溃隔离和 reattach 通过。
- [ ] Actor 队列、ReAct、取消和恢复通过。
- [ ] 模型 tool_call 与 slash 直调共享 Tool Runtime。
- [ ] ToolError、quota、耗时和 trace 统计可重放。
- [ ] Account 读写角色、credential lease、JSONL 校验和 smoke 通过。
- [ ] BM25 手算对拍、CJK tokenizer、混合召回和 reranker 通过。
- [ ] 自主/非自主压缩通过边界与重启测试。
- [ ] startup/TUI 可完成真实交互流程。
- [ ] CI 全部 required checks 成功。
- [ ] 所有 package README 和根 `AGENTS.md` 规则被遵守。

## 4. 文档索引

### 入口与纪律

| 文档 | 用途 |
|---|---|
| [README.md](../../README.md) | 项目总览、当前基线、构建命令 |
| [AGENTS.md](../../AGENTS.md) | 全仓 Agent 工作纪律和完成定义 |
| [code-changes.md](code-changes.md) | 当前代码骨架、API、测试和 commit 建议 |
| [本交接文档](13-goal-handoff.md) | 下一会话 Goal 提示词和索引 |

### 需求与架构

| 文档 | 用途 |
|---|---|
| [00-requirements.md](00-requirements.md) | R-001～R-027、NFR-001～NFR-013 |
| [01-plan.md](01-plan.md) | 架构方案对比、模式与推荐决策 |
| [02-architecture.md](02-architecture.md) | 四 exe 拓扑、进程/IPC、运行时流程、公共 contract |
| [03-domain-model.md](03-domain-model.md) | DDD 限界上下文、聚合、事件和读模型 |
| [04-module-design.md](04-module-design.md) | 模块目录、接口、Tool/Account/Knowledge 设计 |

### 核心语义

| 文档 | 用途 |
|---|---|
| [05-events-and-compression.md](05-events-and-compression.md) | 事件信封、seq、trace、压缩红线和 checkpoint |
| [09-account-management.md](09-account-management.md) | Account 聚合、JSONL、capability binding、smoke |
| [10-tool-requests-and-hybrid-memory.md](10-tool-requests-and-hybrid-memory.md) | slash/tool error/timing、BM25、RRF、reranker |
| [11-account-contract-and-role-usecases.md](11-account-contract-and-role-usecases.md) | Account 公共契约、Hub/Sessiond 用例和权限测试 |

### 交付与验证

| 文档 | 用途 |
|---|---|
| [06-milestones-kanban.md](06-milestones-kanban.md) | M0～M10 看板、依赖和排期 |
| [07-v-model-traceability.md](07-v-model-traceability.md) | 需求、模块、测试、UAT 追溯 |
| [08-development-guide.md](08-development-guide.md) | 开发顺序、CLI、配置、编码约束和 Review |
| [12-ci-and-build-gates.md](12-ci-and-build-gates.md) | 领域 CI、Hub/Sessiond/README 门禁、Windows artifact |
| [14-account-provider-configuration.md](14-account-provider-configuration.md) | DeepSeek/embedding/rerank 入参与 Claude Code JSON 对照 |

### 配置、公共契约与 CI

| 路径 | 用途 |
|---|---|
| `pkg/contracts/account/v1` | Account 公共 DTO、Reader/Admin、method、error |
| `pkg/contracts/credential/v1` | credential lease |
| `pkg/contracts/tool/v1` | invocation mode、ToolError、Timing |
| `pkg/contracts/knowledge/v1` | hybrid retrieval DTO |
| `pkg/contracts/session/v1` | Session 公共引用基线 |
| [config/README.md](../../config/README.md) | 账户与真实 API smoke 填写说明 |
| [llm-accounts.example.jsonl](../../config/llm-accounts.example.jsonl) | chat/embedding/rerank/search 账户模板 |
| [llm-smoke.example.jsonl](../../config/llm-smoke.example.jsonl) | V 模型 API smoke 用例 |
| [.github/workflows/ci.yml](../../.github/workflows/ci.yml) | 领域 CI、门禁和 Windows 构建 |
| [.githooks/pre-commit](../../.githooks/pre-commit) | 本地 commit build 基线 |
| `scripts/check-package-readmes.sh` | CI package README 检查 |
| `scripts/check-package-readmes.ps1` | Windows 本地 package README 检查 |

### Go 源码索引

| 路径 | 当前内容 |
|---|---|
| `cmd/{hub,sessiond,startup,tui}` | 四个可构建入口骨架 |
| `internal/accounts/{domain,app,adapters}` | Account 聚合、应用端口、Hub 内部 adapter 位置 |
| `internal/hub` | Hub Account 读写 allowlist |
| `internal/sessiond` | Sessiond Account 只读 allowlist |
| `internal/{conversation,eventledger,contextmgr}` | 会话、顺序、压缩领域骨架 |
| `internal/{tooling,knowledge,llm}` | 工具、混合召回、provider 领域骨架 |
| `internal/{sessionruntime,observability,operator,platform,bootstrap}` | 运行时、观测、前端、平台、装配骨架 |

查看全部 package 文档：

```powershell
Get-ChildItem -Recurse -Filter README.md | Where-Object {
    $_.DirectoryName -like '*\cmd\*' -or
    $_.DirectoryName -like '*\internal\*' -or
    $_.DirectoryName -like '*\pkg\*'
} | Select-Object -ExpandProperty FullName
```

## 5. 下一会话启动方法

1. 新会话将工作目录设为 `G:\Project\vibe_agent`。
2. 选择 Goal 模式。
3. 使用第 2 节完整提示词作为 Goal 目标。
4. 让 Agent 第一轮先输出：
   - 当前 git 状态；
   - 已完成/未完成核对；
   - 本轮 M1/M2 的具体实施切片；
   - 预计验证命令。
5. 后续轮次允许 Agent 自动继续，但遇到改变公共契约语义、引入外部依赖或真实付费 API 冒烟时必须请求确认。
