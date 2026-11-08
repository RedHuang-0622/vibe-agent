# 事件、Seq 与上下文压缩规范

## 1. 事件信封

`messages.jsonl` 每行是一条完整事件。建议最小信封：

```json
{
  "schema_version": 1,
  "session_id": "sess-a",
  "seq": 7,
  "event_id": "sessiona-seq-7",
  "type": "tool.completed",
  "created_at": "2026-11-04T15:30:00.123+08:00",
  "trace_id": "trace-sess-a-1",
  "causation_seq": 5,
  "call_id": "call-01J...",
  "visibility": "conversation",
  "status": "succeeded",
  "payload": {
    "tool_name": "calculator",
    "tool_kind": "local",
    "invocation_mode": "direct",
    "result": {
      "value": 42
    },
    "timing": {
      "queue_ms": 1,
      "validation_ms": 0,
      "execute_ms": 2,
      "total_ms": 3
    }
  }
}
```

字段说明：

| 字段 | 必需 | 说明 |
|---|---|---|
| `schema_version` | 是 | 信封结构版本 |
| `session_id` | 是 | 会话身份 |
| `seq` | 是 | 会话内唯一、连续、递增 |
| `event_id` | 是 | 规范化为 `<session_id>-seq-<seq>` |
| `type` | 是 | 稳定事件类型 |
| `created_at` | 是 | 审计时间，不能替代顺序 |
| `trace_id` | 视事件 | 同一用户 turn 的链路身份 |
| `causation_seq` | 视事件 | 直接触发它的同会话事件 seq |
| `call_id` | 视事件 | tool 请求/结果配对和幂等键 |
| `visibility` | 是 | `conversation`、`internal`、`audit` |
| `status` | 视事件 | `succeeded`、`failed`、`canceled` 等 |
| `payload` | 是 | 类型相关内容，大小受限 |

`visibility=internal` 的压缩器 LLM 调用是真相源中的事件，但不作为普通对话上下文回灌给用户模型。

## 2. Seq 发放协议

### 2.1 单写者

只有该会话的 sessiond 拥有 event append 权限。hub/TUI/工具实现不得持有文件句柄直接写入。

发送流程：

```text
actor command
  -> validate state
  -> build draft
  -> acquire single-writer lock
  -> last_seq := read verified watermark
  -> seq := last_seq + 1
  -> encode one-line JSON
  -> append
  -> flush
  -> durable sync (configurable policy)
  -> acknowledge event ref
  -> update in-memory watermark
  -> notify hub
```

如果 append/flush/sync 失败：

- 不向 Actor 确认事件成功；
- 会话进入可重试/失败状态；
- 不继续发起依赖该事件的模型请求；
- 重启时以文件最后一条合法记录为准重新计算 seq。

### 2.2 连续性和隔离

不变量：

```text
for each session:
  committed_seq[1] == 1
  committed_seq[i] == committed_seq[i - 1] + 1
  event_id == session_id + "-seq-" + seq
```

示例：

| Session | seq | event_id |
|---|---:|---|
| sessiona | 1 | sessiona-seq-1 |
| sessionb | 1 | sessionb-seq-1 |
| sessiona | 2 | sessiona-seq-2 |
| sessionb | 2 | sessionb-seq-2 |

两个 `seq=1` 完全合法，因为查询、trace、文件路径、日志和事件 ID 都带 `session_id`。

### 2.3 崩溃恢复

启动时：

1. 获取独占 session lock。
2. 逐行解析 JSONL。
3. 校验事件 ID、连续 seq、schema 版本。
4. 如果末尾是不完整行，应隔离/记录为可恢复损坏，不将其作为已提交事实。
5. 从最后的合法 seq + 1 继续。
6. 重放队列、turn 和压缩 checkpoint。

在无法区分“半行损坏”与“磁盘损坏”时，默认不猜测、不盲目截断；先备份报告并要求显式恢复策略。首版可提供 `session repair`，但必须单独命令、显示将丢弃的范围。

## 3. 因果链与 Trace

### 3.1 关联规则

- 一个用户 turn 分配一个 `trace_id`。
- 直接派生事件使用 `causation_seq` 指向触发事件。
- tool 请求/结果通过同一 `call_id` 关联。
- `parent span` 不需要额外数据库；可由 `causation_seq` 还原树。
- 跨会话查询必须带 `session_id`，不能把不同会话的 seq 裸比较。

### 3.2 示例链路

```text
sessiona-seq-20 user.message.accepted
  └─ sessiona-seq-21 turn.started
      └─ sessiona-seq-22 llm.requested
          └─ sessiona-seq-23 llm.completed
              └─ sessiona-seq-24 tool.requested (call_id=c1)
                  └─ sessiona-seq-25 tool.completed (call_id=c1)
                      └─ sessiona-seq-26 llm.requested
                          └─ sessiona-seq-27 llm.completed
                              └─ sessiona-seq-28 assistant.message.completed
                                  └─ sessiona-seq-29 turn.completed
```

若工具在 `sessiona-seq-24` 请求后进程崩溃、重启后结果未知：

- 使用 `call_id` 和工具幂等键查询状态；
- 不重复执行不幂等副作用；
- 根据查询结果补写 `tool.completed` 或 `tool.failed`；
- 该事件的 seq 由重启后的 sessiond 重新分配，不能伪造过去的 seq。

## 4. 投影

### 4.1 Trace Index

从真相源提取：

- `session_id`
- `seq`
- `event_id`
- `type`
- `trace_id`
- `causation_seq`
- `call_id`
- `tool_name`
- `tool_kind`
- `invocation_mode`
- `status`
- `reason_code`
- `queue_ms` / `execute_ms` / `total_ms`
- retrieval `channels_used`、index version、embedding/rerank model fingerprint、degraded flags

查询 `trace_id` 时按 seq 排序，按 causation 构建树；查询 `call_id` 时返回请求、结果、直接父事件和相关 turn。

### 4.2 工具统计

至少生成：

```json
{
  "watermark_seq": 203,
  "by_tool": [
    {
      "tool_name": "search",
      "tool_kind": "remote",
      "calls": 12,
      "succeeded": 9,
      "failed": 3,
      "avg_total_ms": 184,
      "p50_total_ms": 122,
      "p95_total_ms": 451,
      "failure_reasons": {
        "timeout": 2,
        "quota_exhausted": 1
      }
    }
  ]
}
```

统计口径：

- `calls` 以 `tool.requested` 计数；
- `succeeded` 以对应 `tool.completed` 计数；
- `failed` 以对应 `tool.failed` 计数；
- 请求没有结果时显示 `in_flight`/`orphaned`，不能静默当作成功；
- 失败原因使用稳定枚举，原始错误文本不参与聚合主键。
- 耗时必须来自 `tool.completed/failed.payload.timing`；缺失样本单列未知，不能用 0 填充；
- `invocation_mode=model|direct` 分开统计，并支持汇总。

### 4.3 Slash 直调事件链

```text
sessiona-seq-101 user.tool_command.accepted(tool_name=memory_search, raw_request="...")
sessiona-seq-102 turn.started(kind=direct_tool, trace_id=...)
sessiona-seq-103 tool.requested(call_id=c9, invocation_mode=direct)
sessiona-seq-104 tool.failed(
  call_id=c9,
  error={code:quota_exhausted,retryable:false,reset_at:...},
  timing={queue_ms:1,validation_ms:1,execute_ms:87,total_ms:89}
)
sessiona-seq-105 turn.completed(status=failed)
```

如果工具成功，第 104 条为 `tool.completed`，其 result 可包含 memory retrieval channels/ranks/index version。直调不会隐式产生新的 `llm.requested`。

### 4.4 工具异常

- `tool.failed` 的 `error` 使用稳定 `code`，至少包括 `unknown_tool`、`invalid_request`、`quota_exhausted`、`rate_limited`、`authentication_failed`、`timeout`、`dependency_unavailable`。
- `quota_exhausted` 可包含 `scope/remaining/limit/reset_at`，但不得包含 provider secret 或未脱敏 body。
- 模型工具调用失败时，同一结构化 error 作为 tool result 回灌模型；直调失败时直接返回 TUI/CLI。
- provider 原始 request id 可保留；自由文本放在诊断字段，不作为统计主键。

## 5. 压缩模型

### 5.1 两种触发

| 触发 | 来源 | 行为 |
|---|---|---|
| 自主压缩（`autonomous`） | ReAct Loop 预检时 token 达到阈值 | 运行时自主选择安全 `red_line_seq` 并提交压缩请求 |
| 非自主压缩（`external`） | CLI/TUI/Hub 控制命令 | 使用指定 `through-seq`，或请求系统选择安全值后由外部触发 |

两者都必须走 `CompressionService`，不允许各写一套摘要逻辑。

### 5.2 红线定义

`red_line_seq` 是**被摘要覆盖的最后一个源事件 seq**。

约束：

- 必须小于等于预检时已提交的最大 seq。
- 必须位于安全事件边界。
- 不得跨越未完成的 `tool.requested`。
- 不得把 accepted 但未 consumed 的用户消息纳入不可恢复摘要。
- 不得指向尚未持久化的“未来”事件。
- 压缩事件自身使用新的 seq，不能修改源事件的 seq。

示例：

```text
源事件 1..100
red_line_seq = 87
source_from_seq = 1
source_to_seq = 87
compression.requested 的 seq = 101
summary 提交为 102
有效上下文 = summary(1..87) + 事件 88..102 中可见会话事件
```

压缩器 LLM 的内部调用如果写入真相源，使用 `visibility=internal`；它们仍占 seq，但不会进入正常对话上下文。

### 5.3 安全切断算法

```text
chooseSafeCutoff(latestSeq, targetSeq, maxEvents):
  upper := min(latestSeq, targetSeq)
  earliestUnconsumed := first seq of accepted-but-not-consumed user message
  if earliestUnconsumed exists:
    upper := min(upper, earliestUnconsumed - 1)
  if upper < 1:
    return none
  for seq := upper down to 1:
    if seq is after a completed turn or after a completed tool call:
      if no accepted-but-not-consumed message in [seq+1, latestSeq]:
        return seq
  return none
```

实现可以使用事件索引而不是逐条扫描。关键点是：先按 `seq` 筛选，再按事件类型边界确认；不得先切成 token 字符串再猜 `seq`。

`earliestUnconsumed` 上限确保尚未进入任何 turn 的用户消息即使位于候选红线之前也不会被摘要“吞掉”；如果该消息必须在当前 turn 前消费，应先完成消费事件，再重新选择红线。

推荐保留尾部事件数：至少最近一个未完成 turn 的所有事件，以及最近 N 条对话可见事件中的完整 tool 配对。具体 `N` 由配置注入。

### 5.4 压缩提交

`compression.requested` payload：

```json
{
  "trigger": "autonomous",
  "red_line_seq": 87,
  "source_from_seq": 1,
  "source_to_seq": 87,
  "previous_checkpoint_seq": 0,
  "policy_version": "v1"
}
```

`compression.completed` payload：

```json
{
  "source_from_seq": 1,
  "source_to_seq": 87,
  "red_line_seq": 87,
  "summary": "...",
  "summary_ref": "ctx/sess-a/checkpoint-2",
  "source_digest": "sha256:...",
  "tokens_before": 18230,
  "tokens_after": 2310,
  "previous_checkpoint_seq": 0
}
```

提交前校验：

1. 源范围仍存在且 digest 未变。
2. `red_line_seq` 没有推进到未来。
3. 摘要没有包含禁止回灌的内部字段或密钥。
4. 新 checkpoint 的 `committed_seq` 大于此前 checkpoint。

失败时追加 `compression.failed`，不推进有效 checkpoint。

当前实现会在提交 `compression.completed` 前重新读取事件并比对
`source_digest`；不一致时写 `compression.failed(reason_code=source_digest_mismatch)`。
读取 checkpoint 时同样校验 digest，损坏或过期摘要回退到更早的合法 checkpoint。
安全边界只包含已完成 turn 或已完成/失败 tool call，不在 assistant 消息后切断。

## 6. 有效上下文构建

```text
latestSuccessfulCheckpoint(session)
if none:
    candidate = conversation-visible events [1 .. requestedSeq]
else:
    candidate = checkpoint.summary +
                conversation-visible events [checkpoint.source_to_seq+1 .. requestedSeq]
+ pinned system policy
- internal-only compression transport events
- events with retention=exclude
```

注意：

- 不从压缩事件的 `created_at` 推断有效范围。
- 不从 token 数反推红线。
- 不把 `compression.requested` 自身的 seq 当作 `source_to_seq`。
- 若 checkpoint 的摘要和源事件不一致，回退到更早的 checkpoint 或从头构建，并报告 degraded。

## 7. 自主压缩的状态机

```text
Idle/Running
  -> preflight
  -> over threshold?
       no: continue model call
       yes: compute red_line_seq
            -> append compression.requested
            -> summarize
                 success: append compression.completed
                          rebuild context
                          continue model call
                 failure: append compression.failed
                          use original context if within hard limit
                          else return context_overflow
```

自主压缩不能在 `red_line_seq` 选择后让新用户消息插入源范围。Actor 串行化确保这个窗口有明确顺序；如果需要接收新消息，先让它们以更大 seq 落在红线之后。

## 8. 压缩边界测试

必须覆盖：

- 切断发生在 tool request 之前或 result 之后；
- 不切断 pending tool；
- 不吞未消费用户消息；
- 两个 session 的相同 seq 不串流；
- checkpoint 失败不改有效上下文；
- checkpoint 成功后重启仍得到相同的有效上下文；
- 删除投影后从 JSONL 重建一致；
- 自主与非自主压缩产生同样的审计字段；
- 压缩器 LLM 事件可见性不污染普通上下文。

## 9. 可恢复观察与前端窗口

事件页和前端窗口都以 `seq` 为唯一 cursor：

```text
snapshot(tail_limit)
  -> tail + watermark + has_more_before

history(before_seq=WindowFirstSeq, limit)
  -> ascending older page + has_more_before

watch(after_seq=last_applied_seq, wait_ms)
  -> events + cursor + has_more + timed_out + resync_required
```

规则：

- snapshot 只替换 tail；已有 older 和旧 tail 中仍早于新 tail 的事件保留。
- watch 只有在前一页被应用后才推进 `after_seq`，重复页按 seq 跳过。
- `has_more=true` 表示当前页后还有积压，应立即用 `cursor` 继续读取。
- `timed_out=true` 只表示等待窗口内没有新事件，不代表断线或 stop。
- `resync_required=true` 表示 cursor 与当前账本不连续或超前；调用方必须
  重新 snapshot，不能猜测缺失的 tool/model 事件。
- 进程快照中的 `running/idle` 不能推断当前 turn；turn 运行态只由
  `turn.started` 和 `turn.completed` 更新。
- tail/older 合并后必须按 seq 升序、无重复且无空洞；同一会话事实不能
  被前端缓存改写。
