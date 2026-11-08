# Repository Agent Requirements

本文件适用于整个仓库。任何 Agent 在修改代码、测试、CI 或文档前都必须阅读并遵守；更深层的 `AGENTS.md` 可以增加更严格规则，但不能放宽本文件约束。

## 1. 工作前检查

1. 先阅读根 `README.md`、本文件和与任务相关的 `docs/agent-runtime/*`。
2. 检查 `git status --short`，不覆盖、不回滚无关用户改动。
3. 从现有接口和领域模型开始设计，禁止先写跨层具体实现。
4. 变更前确认目标包、公共契约、读写角色、测试门禁和需要更新的 package README。
5. 新依赖必须有明确必要性；首版不引入 Agent 编排、CLI/TUI、ORM、RAG 或工作流框架。

## 2. 架构不变量

- 会话隔离是 OS 进程：每个活跃 Session 对应一个独立 `sessiond.exe`。
- Hub 管理 Sessiond、路由、AccountStore 和共享 KnowledgeService，但不写 Session 事件。
- Sessiond 只服务一个 Session，是该 Session `messages.jsonl` 的唯一写者和 seq 发放者。
- Actor 是 Sessiond 内的串行命令语义，不是会话隔离边界。
- `messages.jsonl` 是 Session 唯一真相源；trace、统计、快照、上下文都是可重建投影。
- 事件顺序只按 `seq`，时间戳只用于审计展示。
- 不允许用 goroutine 代替 Session 进程边界，也不允许非 Actor 路径写 Session 状态。

## 3. 公共契约

- 跨 Hub/Sessiond/TUI 的稳定 DTO、method、enum 和错误码放 `pkg/contracts/<domain>/<version>`。
- `pkg/contracts` 只能依赖标准库，禁止 import `vibe-agent/internal/*`。
- 领域实体、Repository、JSONL loader、provider SDK 类型不得进入公共契约。
- 破坏性变更必须创建新版本包，例如 `v2`；不得改变已发布 v1 字段语义。
- 公共契约 DTO 必须使用显式 JSON tag，不能暴露 secret、文件路径或存储句柄。
- 每次修改公共契约，必须同步契约测试、双端 fixture、package README、V 模型和变更说明。

## 4. Account 权限

- Hub 获得 `accountv1.Admin`，具备 Account 读和写权限。
- Sessiond 只获得 `accountv1.Reader`，只能 `Get` 和 `ResolveForInvocation`。
- Sessiond 禁止 list/create/update/set-default/enable/disable/archive/rotate-ref/validate/smoke。
- 权限必须同时由构造注入的最小 interface、IPC token scope、server method allowlist 保证。
- Sessiond 禁止 import `internal/hub` 或 `internal/accounts/adapters`。
- 账户写操作必须使用 expected revision，并记录 actor、action、account_id、before/after revision 和 changed fields。
- 明文 key 只能通过 CredentialRef/短期 lease 在调用瞬间解析，禁止写日志、事件、trace、TUI 或 config snapshot。
- 真实 API smoke 必须 opt-in，默认 CI 只使用 fake server。

## 5. 工具规范

- 模型 tool_call 与 `/<toolname> <req>` 必须走同一个 Registry、参数校验、授权和 Tool Runtime。
- 直调必须先记录 `user.tool_command.accepted`，再创建 direct-tool turn。
- Tool Runtime 是 tool event 的唯一生产者，具体工具不得直接写 Event Ledger。
- 每个成功、失败、超时和取消事件必须包含 queue/validation/execute/total 耗时。
- 耗时使用 Go monotonic clock，不通过 wall-clock 相减。
- ToolError 使用稳定 `code`，至少支持 quota_exhausted、rate_limited、authentication_failed、timeout 和 dependency_unavailable。
- provider 原始错误必须脱敏；自由文本不能作为统计主键。
- 有副作用工具必须支持 idempotency key。

## 6. Memory / Knowledge 规范

- memory 的 BM25 必须自行实现，不调用现成 BM25/RAG 框架。
- BM25 至少包含 tokenizer、倒排索引、IDF、doc length/avgdl、k1/b 和稳定 tie-break。
- 默认中文 tokenizer 使用 CJK unigram+bigram；Tokenizer 变化必须提升 index version。
- dense 检索必须按 embedding model/dimension/normalization fingerprint 隔离。
- default hybrid 顺序：BM25 topK -> dense topK -> RRF -> optional reranker。
- strict/degraded 必须显式配置并返回实际 channels/warnings，禁止静默降级。
- 更新索引必须使用 copy-on-write 或原子 snapshot，查询不能看到半更新状态。
- embedding/rerank account 通过 capability binding 独立选择，不能盲目复用 chat key/endpoint。

## 7. 包 README 纪律

每个包含 Go package 的目录必须存在 `README.md`，并至少包含：

```text
# <package path>
## Role
## API / Methods
## Boundaries
## Verification
```

要求：

- Role 说明包负责什么、不负责什么。
- API / Methods 列出导出类型、函数、接口和方法；没有导出 API 时明确写“当前没有导出 API”。
- Boundaries 说明依赖方向和禁止行为。
- Verification 给出可复制的测试/构建命令。
- 新增、删除或修改导出 API 时，必须在同一变更中更新对应 README。
- CI 使用 `scripts/check-package-readmes.sh` 检查包目录、README 和必需章节。

## 8. 测试与 CI

提交前至少运行：

```powershell
go build ./...
go test -count=1 ./...
go vet ./...
```

涉及并发、Actor、进程、索引或缓存时还必须运行：

```powershell
go test -race -count=3 ./...
```

CI 分区：

- `baseline / build`：每次 push/PR 的 build/test 基线。
- `domain / contracts`：公共契约和依赖方向。
- `domain / accounts`：Account 聚合/端口。
- 其他领域 job：按 `internal/<domain>` 独立测试。
- `gate / hub`：Hub 角色、控制面与管理测试。
- `gate / sessiond`：Sessiond 角色、运行时与只读 Account 测试。
- `gate / repo-policy`：package README 纪律。
- `gate / quality`：vet + race。
- `baseline / windows-package`：Windows 四 exe artifact。

不得把 Hub 与 Sessiond 的权限测试合并到同一 job。新增领域时必须加入领域 CI matrix。

本地提交前基线：

```powershell
git config core.hooksPath .githooks
git hook run pre-commit
```

## 9. 编码约束

- Go 1.25+，优先标准库。
- 公共 API 必须由 package README 解释；实现对应行为时补齐 GoDoc 注释，内部实现保持小而清晰。
- 构造依赖显式注入，禁止业务包级可变全局状态。
- 错误不能 `return nil, nil`；不得吞异常。
- 代码先定义消费方所需最小接口，再实现 adapter。
- domain 不依赖 infrastructure；`pkg/contracts` 不依赖 `internal`。
- 函数优先 ≤50 行，文件优先 ≤500 行；超限需要明确理由。
- 不硬编码 secret、API key、密码或真实 endpoint。
- 保持 Windows 为首要平台，平台差异放 `internal/platform` 或 build tags。

## 10. 文档与变更记录

- 行为、接口、权限、数据格式或 CI 变化必须同步相关 `docs/agent-runtime/*`。
- 新增需求分配 R/NFR ID，并更新 V 模型追溯矩阵。
- 新增公共 method 必须有契约测试与使用方边界说明。
- 每次编码任务在 `docs/agent-runtime/code-changes.md` 记录文件、API、测试和回滚信息。
- 不写空泛承诺；性能/安全声明必须有测试或数据。

## 11. Git 纪律

- 不使用 `git reset --hard`、`git checkout --` 等破坏性命令。
- 只提交与任务相关的路径，不覆盖无关未提交改动。
- 用户未明确要求时不自动 commit/push。
- commit message 使用 Conventional Commits；逻辑变更按可回滚边界拆分。
- 提交前必须通过 pre-commit；如果 hook 失败，先修复，不绕过。

## 12. 完成定义

一个任务只有同时满足以下条件才算完成：

- 验收标准和测试已实现；
- build/test/vet/race 要求通过；
- Hub/Sessiond/公共契约边界未被破坏；
- 每个受影响 package 的 README 已更新；
- 相关开发文档与 V 模型已同步；
- 没有 secret、debug bypass 或未说明的临时代码；
- 变更可由回滚点恢复，并在 `code-changes.md` 记录。
