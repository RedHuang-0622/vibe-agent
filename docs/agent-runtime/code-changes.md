# 代码变更摘要

日期：2026-11-04  
范围：从四入口/contracts 骨架实现完整本地 Agent CLI 运行时，并完成故障与 UAT 回归。

## 实现概览

| 领域 | 实现 |
|---|---|
| Event Ledger | `messages.jsonl` 单写者、连续 seq、EventID、fsync、尾半行隔离与重放 |
| Conversation | 单 Actor 串行命令、pending 队列、恢复、取消、ReAct、最大步数、direct-tool turn |
| Hub/Sessiond | loopback framed IPC、每 Session 独立进程、session lock、generation/metadata/health 校验、reattach、优雅 stop |
| Tooling | Registry、JSON schema 子集、direct adapter、统一 Tool Runtime、稳定 ToolError、monotonic timing、panic 收敛 |
| 默认工具 | calculator、search、read_docs、todo、weatherapi、memory_write/search/update |
| Account/LLM | JSONL repository、revision、audit identity、credential lease、capability routing、binding timeout、validate/smoke |
| Knowledge | 自研 CJK BM25、dense fingerprint、RRF、reranker、strict/degraded、memory record/update 恢复 |
| Compression | seq 红线、安全边界、pending tool/未消费消息保护、digest 校验、checkpoint 重建 |
| Observability | trace、failure/invocation mode、0ms timing、avg/P50/P95、in-flight/orphaned、删除投影重建 |
| Operator | Hub CLI、TUI、消息/turn 关联 cursor、session 切换、slash、trace/stats/compress/accounts |
| Platform/startup | Windows 文件锁、后台 Hub 幂等拉起、当前用户启动项 install/run/status/uninstall |
| Contracts | account/credential/session/tool/knowledge v1，显式 JSON tag，标准库依赖 |

## 2026-11-08 隐藏 PTY、会话 Todo Board 与真实工具链

| 文件/领域 | 变更 | 原因 |
|---|---|---|
| `tools/todo/board.go` | 新增会话级 `todo_board`：list/get/create/update/delete、状态/优先级、tombstone、expected_version、幂等键 | 运维 board 需要完整 CRUD，而不是只增改旧 todo |
| `internal/sessiond/runner.go` | 在当前 `SessionDir/todo-board.jsonl` 装配 board 工具 | 每 Session 一个 board，调用方不能选择跨会话路径 |
| `internal/conversation/prompt.go` | system prompt 增加稳定工具策略和 schema 摘要 | 模型知道先读后写、version 与幂等约束；完整 schema 仍由 tools 字段提供 |
| `internal/operator/{cli,tui}.go` | 回合中显示 tool request/result/failure；新增 `/board` | 隐藏 PTY 必须能看到工具闭环，而不只看最终文本 |
| `internal/llm/chat_protocol.go` | OpenAI 工具续轮改为标准 message 形状；新增 Anthropic Messages adapter | 修复真实 provider 的多步 tool_call/tool_result 互操作 |
| `scripts/pty_tui_probe.py` | pywinpty 隐藏 PTY 驱动 TUI 并保存原始输出 | 复现 split harness 的 PTY snapshot 断言方式 |
| `scripts/tui-real-api-smoke.ps1` | 真实 provider opt-in；验证 5 个 board action、事件配对、seq、secret scan；鉴权失败记 BLOCKED | WP-4 收窄后的单 TUI 真实工具链剧本 |
| `scripts/tui-toolchain-smoke.ps1` | fake Anthropic-compatible provider 的 PTY CI 基线 | 无外部凭据时仍可回归传输与 CRUD 链路 |
| `scripts/package-windows.ps1` | 构建四 exe Windows 压缩包、复制配置、生成内外 SHA256 并扫描明文 key | 提供可本地验收、可上传 GitHub Release 的无密钥发布物 |

### API / Compatibility

- 新增 `todo_board` tool name，旧 `todo` v1 保留。
- `anthropic_messages_v1` 为 account binding 新增协议值；不改现有
  `openai_compatible` v1 字段语义。
- TUI 新增 `/board [list|JSON operation]`。
- 不新增公共 DTO；board store 与工具参数留在内部/tool contract。

### Tests

- board CRUD、状态/优先级、幂等重放、version conflict、删除后 list、重启恢复；
- schema 摘要与完整 tool schema 双通道；
- OpenAI assistant.tool_calls/tool message 请求形状；
- Anthropic tool_use/tool_result 请求与 text/tool_use 响应；
- TUI 工具事件只按 seq 渲染一次；
- hidden PTY -> fake Anthropic-compatible provider -> todo_board 五步 CRUD；
- real API 脚本 opt-in；本轮外部凭据返回 `authentication_failed`，
  按 WP-4 记 BLOCKED，未伪造 PASS。

### Rollback

1. 删除 `todo_board` 注册与 `todo-board.jsonl`，保留旧 `todo` 数据路径。
2. 回退 system prompt 摘要为原 context text。
3. 回退 gateway 的 Anthropic 分支与 OpenAI message shape 转换。
4. 删除 PTY Python harness 与两个 smoke 脚本；不触碰 Session JSONL。

### Verification

| 命令/场景 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go test -count=1 ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race -count=3 ./...` | 通过 |
| package README check | 32 个 package 通过 |
| `scripts/tui-toolchain-smoke.ps1` | PASS，隐藏 PTY + fake Anthropic-compatible provider + 五步 board CRUD |
| `scripts/uat.ps1` | PASS，OpenAI-compatible fake chat/tool loop 与既有 UAT 无回归 |
| `git hook run pre-commit` | 通过 |
| `scripts/tui-real-api-smoke.ps1 -RunRealApi` | BLOCKED：provider 在 `llm.requested` 后返回 `authentication_failed`；未记 PASS |

## 2026-11-08 可恢复观察面与会话缓存改进

依据成熟产品的 transport/session-cache 基线，对当前实现补齐以下链路：

| 文件/领域 | 变更 | 原因 |
|---|---|---|
| `pkg/contracts/session/v1` | 新增 `session.snapshot/watch/history` DTO；events 增加 `cursor/has_more/resync_required/timed_out` | 让客户端区分已追平、积压、超时和不可恢复缺口 |
| `internal/eventledger` | `ChangeSignal` 在持久化 append 后唤醒观察者；`ReadRange` 与写入共用互斥 | 长轮询不忙等，查询只看到完整已提交行 |
| `internal/sessiond` | 实现最新快照、`before_seq` 历史页、最多 30 秒 watch；返回连续性和分页元数据 | 为 TUI/CLI 提供可恢复观察面 |
| `internal/hub` | 将 snapshot/watch/history 路由到目标 sessiond，保持 admin token 边界 | TUI 仍只连接 Hub |
| `internal/operator` | Client 新增 Snapshot/History/Watch；回合等待改为 cursor 长轮询与有界退避；CLI 新增 events/snapshot/history | 断线后不重复/不跳事件，观察链路可脚本验证 |
| `internal/operator/sessioncache` | 新增 tail+older 缓存、接缝去重、空洞标记和 turn 状态隔离 | 刷新 snapshot 不丢历史，A/B 不串流 |
| `internal/operator/tui.go` | 启动/切换加载 snapshot；新增 `/snapshot`、`/history`、`/older` | 把缓存规则接入实际 TUI，而非只做库内实现 |
| `scripts/smoke-chain.ps1` | 真实 hub/sessiond 临时目录链路冒烟 | 验证 A/B snapshot、watermark、history、watch timeout 和 event_id 隔离 |
| `.github/workflows/ci.yml` | Windows artifact 构建后执行短链路冒烟 | 防回归且避免只靠本地手工验证 |

### API / Compatibility

- `sessionv1.EventsResponse` 新增 additive JSON 字段，既有 v1 字段语义不变。
- 新 method `MethodSessionSnapshot`、`MethodSessionWatch`、`MethodSessionHistory`。
- `operator.Client` 新增 `Snapshot`、`History`、`Watch`。
- `eventledger.Writer` 新增 `ChangeSignal`。
- 新包 `internal/operator/sessioncache`。

### Tests

- contract DTO round-trip；
- event ledger append notification；
- watch 被持久化事件唤醒；
- event page/history pagination；
- snapshot tail+older seam、gap、turn state、session switch；
- `WaitForMessageTurn` 在首个可重试断线后按旧 cursor 恢复；
- Windows `scripts/smoke-chain.ps1` 真实进程冒烟。

### Rollback

1. 移除 `session.snapshot/watch/history` contract 与 Hub/Sessiond 路由。
2. 删除 `internal/operator/sessioncache` 并恢复 TUI 轮询等待。
3. 移除 eventledger `ChangeSignal` 与写入读互斥。
4. 删除 `scripts/smoke-chain.ps1`，不影响事件账本或既有 v1 数据。

## 本轮续审修复

| 文件 | 变更 | 原因 |
|---|---|---|
| `internal/conversation/actor.go` | `turn.completed` 在同一 Actor 命令内释放活动槽；后台回调用可关闭命令投递 | 消除终态落盘后立即压缩/提交的竞态窗口和 goroutine 阻塞 |
| `internal/operator/{cli,tui}.go` | `WaitForMessageTurn` 按 `message_id -> turn_id` 关联；TUI 在 use/new/slash/cancel/compress 后同步 watermark | 防止旧 `turn.completed` 提前结束新消息 |
| `internal/accounts/**` | 支持 `timeout`/`timeout_ms`、严格 JSON 字段、URL/credential ref/binding 校验、actor/request audit | 仓库示例可直接加载，提升账户配置安全边界 |
| `pkg/contracts/account/v1` | `SmokeRequest` 新增 `input`、`timeout_ms` 与 fixture | 文件驱动 smoke 可传递用例内容且保持 v1 向后兼容 |
| `pkg/contracts/account/v1` | 新增 `ThinkingConfig`、`BindingInput.api_key_env`、binding reasoning effort 与安全 Bindings view | Account 可表达 DeepSeek Flash 装配且不暴露 credential |
| `internal/operator/smoke.go` | 新增 smoke JSONL loader、tag/offline/file CLI | 文档命令与实现一致，真实 API 保持显式 opt-in |
| `internal/llm/gateway.go` | capability binding timeout、quota/rate reset 信息映射 | 账户 timeout 不再仅停留在配置中，错误可决策 |
| `internal/llm/gateway.go` | chat 请求按 binding 发送 `thinking`/`reasoning_effort`，默认不发送 temperature | 对齐 DeepSeek `/chat/completions` 示例入参 |
| `internal/accounts/domain/account.go` | 支持官方 `none/low/high/max`，归一化 `minimal/medium/xhigh`，默认 `env:DEEPSEEK_API_KEY` | 与 DeepSeek 当前 API 文档一致，最小配置可运行 |
| `internal/accounts/domain/account.go` | `NormalizeServices` 将 DeepSeek chat binding 复用到缺失的 tool_call | 最小账户只写一份 LLM 配置即可支持 ReAct |
| `internal/llm/gateway.go` | embedding 按 `batch_size` 分批并发送 `dimensions` | 让 embedding 配置真实生效并校验每批结果 |
| `config/llm-accounts.example.jsonl` | chat/tool_call 精简为 `api_key_env + thinking + reasoning_effort` | 默认生成 `deepseek-flash` 且避免重复 endpoint 配置 |
| `config/{deepseek-account,embedding-rerank,claude-code-deepseek.settings}.example.*` | 新增最小账户、embedding/rerank 模板与 Claude Code JSON 示例 | 方便他人直接复制配置，保持 secret 外置 |
| `internal/contextmgr` | checkpoint digest 校验、移除 assistant 非安全边界 | 损坏摘要回退，压缩不切断未完成 turn |
| `internal/knowledge` | update metadata 隔离、rerank 漏项/重复/非有限值校验、scope/query 校验 | 失败不污染内存索引，strict 模式不静默降级 |
| `internal/observability` | timing 使用字段存在性而非 `>0` | 合法的 0ms 样本不被丢弃 |
| `internal/{hub,sessiond,sessionruntime}` | request/response id/version、health generation、reattach identity、stop wait | 防止陈旧 Sessiond 接管和误杀未刷盘进程 |
| `tools/todo` | 保存失败回滚 item/idempotency | 内存状态不能领先持久化事实 |
| `scripts/uat.ps1` | 增加优雅 stop、文件 smoke、TUI 换会话/slash 后 chat | 覆盖原 UAT 未触达的交互和生命周期路径 |
| `scripts/uat.ps1` / `testdata/fake_provider.go` | fake chat/tool_call 使用 DeepSeek Flash binding，并校验 thinking/effort/stream | 防止只改配置、请求体却未接线 |
| `.github/workflows/ci.yml` | Windows artifact 增加 `version.txt` | 满足发布包版本清单要求 |

## API / Methods

| API | 变更 | 兼容性 |
|---|---|---|
| `accountv1.SmokeRequest.Input` | 新增 `json:"input,omitempty"` | 向后兼容 |
| `accountv1.SmokeRequest.TimeoutMS` | 新增 `json:"timeout_ms,omitempty"` | 向后兼容 |
| `accountv1.ThinkingConfig` | 新增 thinking type DTO | 向后兼容 |
| `accountv1.BindingInput.APIKeyEnv` | 环境变量名便捷配置，规范化为 CredentialRef | 向后兼容，不保存 secret |
| `accountv1.BindingInput/BindingView` | 新增 thinking/reasoning effort；规范值 `none/low/high/max` | 向后兼容 |
| `accountv1.AccountView.Bindings` | 新增不含 credential 的安全 binding 视图 | 向后兼容 |
| `app.WithAuditIdentity` | 注入 actor 与 request id | 新函数 |
| `operator.WaitForMessageTurn` | 等待指定 message 消费出的 turn | 新函数 |
| `operator.LoadSmokeCases` / `SmokeCase` | 文件驱动 smoke | 新 API |
| `llm.Error` | 增加 scope/provider/request id/reset | 内部扩展 |
| `sessionruntime.CodedError` | 保留 scope/provider/request id/reset | 内部扩展 |
| `conversation.Actor` | terminal event 原子释放活动 turn | 行为修正，签名不变 |

现有 v1 字段语义未修改；无破坏性 contract 变更。

## 设计模式

| 模式 | 应用 |
|---|---|
| Command | Actor 串行命令与 reply channel |
| Ports & Adapters | Account、LLM、Knowledge、Event Ledger、Platform |
| Strategy | tokenizer、summarizer、provider/reranker/fake clients |
| Factory / Composition Root | `bootstrap` 显式装配四入口 |
| Projector | trace/stats/context 从 JSONL 重建 |
| Repository | Account、Todo、Memory 持久化隔离 |

## 测试与验证

| 命令/场景 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go test -count=1 ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race -count=3 ./...` | 通过 |
| package README check | 31 个 package 通过 |
| contracts 无 `internal/*` 依赖 | 通过 |
| Sessiond 无 Hub/Account adapter 依赖 | 通过 |
| Windows `hub/sessiond/startup/tui.exe` | 四文件构建通过 |
| `scripts/uat.ps1` | A/B seq、20 Session 隔离、A crash/B continue、Hub reattach、stop、ReAct、slash、quota、memory、压缩、投影重建、startup、TUI、file smoke 通过 |
| `git hook run pre-commit` | 在最终门禁复跑 |

新增回归重点：

- Actor 终态后立即压缩；
- parseSlash 任意空白；
- message/turn cursor 与 TUI 切换 session；
- account timeout alias、未知字段、URL/credential/binding capability；
- 文件 smoke input/timeout/offline；
- DeepSeek Flash 默认 model/endpoint/path/timeout/thinking/effort；
- reasoning effort 官方值/兼容别名、最小账户默认 API key env；
- embedding batch/dimensions 请求与 rerank 请求形状；
- Claude Code 严格 JSON settings 示例（不使用 YAML）；
- `TestDeepSeekReasoningEffortCanonicalization`；
- `TestDeepSeekProviderNameIsCanonicalized`；
- `TestJSONLRepositoryMinimalDeepSeekChatBinding`；
- `TestGatewayEmbeddingBatchesAndDimensions`；
- `TestGatewayRerankTopN` 请求体断言；
- `TestDeepSeekAndClaudeCodeExamplesAreUsable`；
- `DEEPSEEK_API_KEY` 到 credential lease 的装配；
- Gateway 请求体与 curl 入参一致，且省略默认 temperature；
- LLM binding timeout、quota reset；
- 0ms latency sample；
- checkpoint digest 与安全边界；
- rerank 漏项、failed update metadata、Todo 保存回滚；
- framed response id、request id 与 protocol version；
- tool handler panic 与非法 JSON/integer。

## 回滚

工作树仍全部未提交，无需破坏性 Git 回滚。若需局部回退，可按领域恢复：

1. Actor 修复：`internal/conversation/{actor,run,compression}.go` 与对应测试。
2. Operator cursor：`internal/operator/{cli,tui}.go` 与 operator tests。
3. Account/smoke：`pkg/contracts/account/v1`、`internal/accounts/**`、`internal/operator/{accounts,smoke}.go`、config fixture。
4. 检索/压缩/todo/stats：对应领域文件与测试。
5. 进程协议：`internal/{hub,sessiond,sessionruntime}`。

## 剩余外部验证

- 仓库当前没有 Git remote，且用户未要求 commit/push，因此 GitHub hosted required checks 尚未运行；本地等价命令全部通过。
- 真实 API smoke 已实现但未执行，需要操作员提供真实账户 JSONL 与 key；默认/fake/offline smoke 已验证。
- Windows 是本地主测平台；Linux 编译由 CI Ubuntu job 在 push 后验证。

## 建议 Commit

```text
feat(runtime): complete recoverable local agent CLI

- implement sessiond event ledger, actor queue, React loop, tools, compression
- add account leases, hybrid memory, trace/stats, startup and TUI flows
- harden generation, timeout, smoke, audit and interaction cursor recovery
- extend Windows UAT and synchronize contracts, READMEs and V-model evidence

Refs: R-001..R-027, NFR-001..NFR-013
```
