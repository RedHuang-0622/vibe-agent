# 开发指南

## 1. 开发环境

- Go 1.25+。
- Windows 10/11 为首要运行环境。
- Git 2.51+。
- 首版不引入 Agent 框架、CLI 框架、TUI 框架、ORM 或工作流引擎。

初始化时设置正式模块路径，然后按 [模块设计](04-module-design.md) 创建目录。不要在仓库根目录写业务逻辑。

仓库级约束见根目录 [AGENTS.md](../../AGENTS.md)。每个 Go package 目录必须有 `README.md`，并在导出 API 变化时同步更新；CI 的 `gate / repo-policy` 会执行结构检查。

跨进程契约统一放在版本化公共包：

```text
pkg/contracts/account/v1
pkg/contracts/credential/v1
pkg/contracts/session/v1
pkg/contracts/tool/v1
pkg/contracts/knowledge/v1
```

该目录只放 DTO、method name、enum、稳定错误码和跨进程 client/server interface。领域实体、Repository、JSONL loader 不允许放入；`pkg/contracts` 不允许 import `internal/*`。

## 2. 本地开发顺序

```text
1. 建立 domain 类型和错误码
2. 写单写者/seq 的 unit + crash tests
3. 建立 hub/sessiond framed IPC
4. 建立 Actor 命令与待发送队列
5. 接入 fake model + fake tools，跑通 ReAct
6. 接入多能力账户、fake embedding/rerank
7. 实现 BM25 + hybrid retrieval
8. 接入真实 tool/LLM/embedding/rerank adapters
9. 接入压缩
10. 接入 trace/stats
11. 接入 TUI 和 startup
12. 运行故障矩阵和 UAT
```

每一步都先定义端口，再由 bootstrap 注入依赖。不要在一个大 `main.go` 中同时处理进程、HTTP、模型、工具和 TUI。

## 3. 可执行文件协作

```text
startup.exe  -> 检查/拉起 hub.exe
tui.exe      -> 连接 hub.exe，负责展示与输入
hub.exe      -> 管理 sessiond.exe 进程，路由命令和事件
sessiond.exe -> 仅服务一个 Session，管理 Actor、JSONL、Loop、压缩、工具、LLM
```

常用开发命令形式：

```powershell
go run ./cmd/hub serve
go run ./cmd/startup run
go run ./cmd/tui
```

构建发布物：

```powershell
go build -o bin/hub.exe ./cmd/hub
go build -o bin/sessiond.exe ./cmd/sessiond
go build -o bin/startup.exe ./cmd/startup
go build -o bin/tui.exe ./cmd/tui
```

Hub 启动 sessiond 时必须解析并传递最终发布目录中的 `sessiond.exe` 路径，不能依赖当前工作目录碰巧存在。

## 4. 配置

首版使用标准库可直接支持的 JSON 配置文件，不把运行时配置写进会话事件。配置分三层：

1. 编译期不可变约束：协议版本、seq 最小值、事件 envelope 版本。
2. 用户配置文件：Hub 端口、数据根、默认账户、压缩阈值、工具权限。
3. 构造注入：测试替换 store、clock、process launcher、model client。

示例概念配置：

```json
{
  "hub": {
    "listen": "127.0.0.1:0",
    "data_root": "%LOCALAPPDATA%/vibe_agent"
  },
  "runtime": {
    "max_steps": 12,
    "max_frame_bytes": 8388608,
    "tool_timeout": "30s"
  },
  "compression": {
    "enabled": true,
    "trigger_ratio": 0.80,
    "keep_tail_events": 20,
    "max_consecutive_failures": 2
  },
  "memory": {
    "bm25": {"k1": 1.2, "b": 0.75},
    "retrieval": {
      "lexical_candidates": 50,
      "dense_candidates": 50,
      "rrf_k": 60,
      "rerank_candidates": 20,
      "allow_degraded": false
    }
  }
}
```

配置解析失败时 fail-fast，不自动使用“看起来差不多”的默认值。

账户不内嵌在主配置：使用 `accounts.jsonl` 的账户快照和 `CredentialRef`。仓库模板：

- [config/llm-accounts.example.jsonl](../../config/llm-accounts.example.jsonl)
- [config/llm-smoke.example.jsonl](../../config/llm-smoke.example.jsonl)

真实文件复制为 `config/llm-accounts.local.jsonl`，只把 key 放入环境变量或系统凭据存储。

## 5. CLI 草案

```text
# 启动和检查
startup.exe install
startup.exe run
hub.exe serve
hub.exe status

# 会话
hub.exe session create --name "项目问答"
hub.exe session list
hub.exe session inspect <session_id>
hub.exe session stop <session_id>

# 聊天和压缩
tui.exe
hub.exe run <session_id> "请阅读 docs 并总结"
hub.exe compress <session_id> --auto
hub.exe compress <session_id> --through-seq 42

# 工具
hub.exe tool list
hub.exe tool call <session_id> calculator '{"expression":"(1+2)*3"}'
/calculator (1+2)*3
/search Go context cancellation
/memory_search 工具调用的 seq 规则

# 账户
hub.exe llm account validate --file config/llm-accounts.local.jsonl
hub.exe llm account list
hub.exe llm account use deepseek-main --capability chat
hub.exe llm account smoke --file config/llm-smoke.local.jsonl --tag real-api
hub.exe llm account smoke --account embedding-main --capability embedding

# 观测
hub.exe trace <session_id> --call-id <call_id>
hub.exe stats <session_id> --group-by tool_name,invocation_mode
```

TUI 优先做交互体验，CLI 保持脚本可测。两者不能各自实现业务规则。

## 6. 编码约束

### Event Ledger

- 禁止 `os.WriteFile` 重写 `messages.jsonl`。
- 禁止业务模块直接 `os.OpenFile(...O_APPEND)`。
- 写一行后 flush/sync 再通知订阅者。
- 事件 payload 有大小上限；超限要拆事件或返回 `validation_error`。
- 测试使用临时目录和可注入 clock；不要用真实用户数据目录。

### Actor

- Actor 是 sessiond 内串行化命令的边界，不是跨会话调度池。
- 外部 I/O 回调必须转成 Actor 命令。
- 每次状态迁移先在内存中校验，再提交事件；事件成功后更新可观察状态。
- 不得从 tool/model goroutine 直接修改队列或 Event Ledger。

### 进程管理

- Hub 的 process map 是控制面的缓存，不是事件真相源。
- 启动 sessiond 前先检查 session lock 和二进制版本。
- sessiond 的 IPC 端点必须支持 hub 重启后重新 attach；stdout/stderr 不能承载业务消息。
- 停止顺序：hub 停止路由 -> sessiond 停止接收 -> 保存/刷盘 -> 退出。
- Windows 必须显式处理 Job Object/进程组，避免 hub 退出后 sessiond 变孤儿。
- 不能用一个“每会话管理 goroutine”代替进程生命周期管理；进程状态属于 hub 的受测 ProcessManager。

### 工具

- 所有工具都通过 Registry 获得，不直接 import 具体实现到 Loop。
- 工具参数通过 JSON Schema 子集校验。
- 所有网络工具必须支持 context cancellation 和 timeout。
- 有副作用工具必须有 `idempotency_key`。
- 工具返回值必须可序列化、可摘要、可脱敏。
- `/toolname req` 与模型 tool_call 必须走同一 Tool Runtime，只通过 `invocation_mode` 区分。
- 所有工具错误映射为 `ToolError`；额度不足不得用泛化 `internal_error` 表示。
- timing 使用 monotonic clock，成功/失败/取消都记录。

### 账户与 LLM

- 不在日志和事件中打印 authorization header、API key、完整 prompt 中的密钥。
- 账户 ID/模型名可以入事件；secret 只能通过 CredentialStore 在调用瞬间解析。
- 重试必须有上限和可观测的 reason；非幂等副作用不能自动重试。
- Hub 账户管理面可注入 `accountv1.Admin`；Sessiond、LLM Gateway、KnowledgeService 只能注入 `accountv1.Reader`。
- Sessiond IPC token 只含 `account:read`，server 必须用 method allowlist 拒绝 `account.admin.*` 写方法。
- Sessiond 不读取 account JSONL、不 list 全部账户，只按 capability/account_id 获取最小 binding。
- credential 使用短期 lease；lease/secret 不进入日志、事件或 trace。
- 按 capability 选择 binding，chat 账户不能默认复用为 embedding/rerank。
- 账户更新必须校验 `expected_revision`；JSONL 文件通过临时文件 + 原子替换更新。
- 真实 API smoke 默认关闭，必须 opt-in；报告不得包含 key。

### Knowledge / BM25

- BM25 是自己实现的项目代码，不能调用外部 BM25/RAG 框架。
- 索引更新使用 copy-on-write 或原子快照，读请求不能看到半更新 postings。
- 查询向量与文档向量必须具有相同 embedding fingerprint。
- Tokenizer 变化必须提升 index version，并重建 BM25 索引。
- reranker 只接收 query 和候选文本，不接收 secret/无关会话全量历史。
- strict / degraded 行为必须显式测试，禁止静默退化为 BM25。

### TUI

- UI 只消费 Hub DTO/事件，不读取 JSONL 文件。
- 断线后以最后 ACK 的 seq 作为 cursor。
- 工具状态、压缩状态、trace 必须显示“来源水位/是否追赶中”。
- 启动、`/use` 和重同步先读 `session.snapshot`；不要用进程状态猜 turn running。
- `session.watch` 使用有界等待（当前最大 30 秒），可重试连接错误才退避重连；
  auth/permission/validation 错误不得自动重试。
- `/history` 与 `/older` 只合并 `seq < tail[0]` 的事件；接缝重复行只保留一次。
- `resync_required=true` 时必须重新 snapshot；不得从最终 assistant 消息补齐工具链。

可脚本化的链路冒烟：

```powershell
powershell -File scripts/smoke-chain.ps1 -BinDir bin
```

该脚本使用临时 `VIBE_AGENT_HOME` 启动真实 hub/sessiond，验证 A/B 会话
snapshot、watermark、history、watch timeout 和 event_id 隔离。

## 7. 日志与脱敏

结构化日志字段：

```text
component
session_id
seq
trace_id
call_id
event_type
status
reason_code
invocation_mode
queue_ms
execute_ms
total_ms
index_version
pid
generation
duration_ms
```

日志不是事实源。对敏感字段：

- 配置输出只显示 `credential_ref`；
- HTTP 请求头默认不记录；
- 错误文本经过正则/结构化脱敏；
- 记录 payload 时只记录 digest 或安全摘要；
- 测试对标准输出、日志文件和 JSONL 做 key 泄漏扫描。

## 8. 测试命令基线

```powershell
go vet ./...
go test ./...
go test -race -count=3 ./...
go test -run TestCompression ./internal/contextmgr/...
go test -run TestCrashRecovery ./internal/sessionruntime/...
go test -run TestBM25 ./internal/knowledge/bm25/...
go test -run 'TestHybrid|TestEmbedding|TestRerank' ./internal/knowledge/retrieval/...
go test -run TestToolTiming ./internal/tooling/...
go test -run 'TestAccountReader|TestSessiondWrite|TestAccountContract' ./pkg/contracts/account/... ./internal/accounts/...
```

工具 fake server、LLM fake server 和进程测试必须默认离线运行。真实 API 测试只在显式启用、提供测试凭据时运行，不能进入默认 CI。

## 9. Review 清单

- 这次改动是否让 hub 写了事件或直接改变了会话业务状态？
- 这次改动是否让非 Actor 路径发放了 seq？
- 是否把 goroutine 当作会话隔离或会话生命周期边界？
- 新工具是否只依赖 ToolHandler，不影响 Loop？
- 新公共 contract 是否位于版本化 `pkg/contracts`，且没有引入 `internal/*`？
- Sessiond 是否只获得 AccountReader，写方法是否在 token/method allowlist 层也被拒绝？
- Slash 直调和模型 tool_call 是否共享 Registry、授权、错误和 timing？
- 工具异常是否有稳定 code，额度不足是否保留 reset/remaining 且不泄漏 secret？
- BM25/向量索引是否原子更新，embedding fingerprint 是否一致？
- strict/degraded retrieval 是否在结果和 trace 中明确？
- 压缩是否包含 `red_line_seq`，是否能处理 pending tool？
- 统计是否明确 in-flight/orphaned 口径？
- 事件和日志是否可能包含 key？
- 重启/崩溃测试是否覆盖新增状态？
- 投影是否可以删除重建？
- TUI 是否绕过 hub 访问内部文件或 sessiond？

## 10. 实现前必须确认的外部契约

以下事项不会改变总体架构，但编码前必须拿到具体协议：

1. DeepSeek Search 的具体 base URL、鉴权方式、请求/响应 schema 和配额错误码。
2. Weather API 的 provider、认证方式、日期/地点参数和限流策略。
3. memory 的作用域定义：workspace/user/global 的默认值和权限。
4. todo 是否与会话共享 workspace，是否允许跨 Session 访问。
5. 首版 CredentialStore 使用环境变量还是 Windows Credential Manager。
6. Hub 对 TUI/CLI 的控制面采用 loopback HTTP 还是本机 named pipe；建议首版 loopback + token，后续封装 transport。Hub 对 sessiond 必须使用每会话本地 named pipe/Unix socket，以支持重连接管。
7. embedding API 与 reranker API 的具体 provider、endpoint、请求/响应 schema、维度与额度错误码。
8. 知识库的默认 scope、chunk size/overlap 和 memory 保留策略。

这些问题不阻塞 seq、进程、工具注册、压缩红线和 trace 的设计，但必须在 M4/M5/M5B 实施前固化。
