# 测试报告

## 概览

| 项目 | 结果 |
|---|---|
| Go 版本 | 1.25.8 |
| 平台 | Windows amd64 |
| `go build ./...` | 通过 |
| `go test -count=1 ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race -count=3 ./...` | 通过 |
| `git hook run pre-commit` | 通过 |
| package README | 31 个 package 通过 |
| Windows 四 exe | 通过 |
| Windows UAT | 通过 |
| 短时 fuzz | tokenizer / calculator / framed IPC 全部通过 |

## 分层结果

| 维度 | 结果 | 关键证据 |
|---|---|---|
| 单元 | 通过 | Event Ledger、Actor、Tool Runtime、BM25、RRF、Account、LLM、stats、operator |
| 契约 | 通过 | account v1 smoke fixture、session/tool/knowledge JSON fixture；contracts 无 internal 依赖 |
| 集成 | 通过 | fake provider、知识库恢复、Hub token scope、sessiond method allowlist、framed client/server |
| Account provider | 通过 | DeepSeek Flash 最小账户、thinking effort 规范化、embedding batch/dimensions、rerank 请求、Claude Code settings JSON |
| 进程 | 通过 | A/B 独立 Sessiond、kill A、Hub reattach、20 Session、stop、A/B seq |
| 故障 | 通过 | JSONL 尾半行、malformed line、lease expiry、Todo 写失败回滚、rerank 漏项、协议 id mismatch |
| 并发 | 通过 | `-race -count=3` |
| Fuzz | 通过 | 3 个目标各 3 秒，无崩溃/不变量失败 |
| 安全 | 通过 | UAT 扫描持久化 JSON/JSONL/log 未发现测试 credential；read_docs traversal/symlink 防护测试 |
| 性能 | 有界验证 | Tool timing 使用 monotonic 结果；0ms 样本保留；P50/P95 可重建；未建立历史 benchmark 基线 |

## 覆盖率

| Package | 覆盖率 |
|---|---:|
| knowledge | 84.3% |
| contextmgr | 82.2% |
| calculator | 80.7% |
| observability | 76.9% |
| llm | 74.9% |
| accounts/domain | 72.1% |
| tooling | 70.4% |
| todo | 67.6% |
| conversation | 63.6% |
| eventledger | 63.5% |
| search | 62.8% |
| accounts/adapters | 60.5% |
| weather | 58.0% |
| read_docs | 56.9% |
| sessionruntime | 52.6% |
| memory | 47.8% |
| accounts/app | 41.5% |
| operator | 28.0% |
| platform | 25.3% |
| hub | 10.7% |
| sessiond | 9.3% |

`cmd/*` 与 `bootstrap` 由 Windows UAT 覆盖，不计 Go statement coverage。
核心进程/CLI 包的 statement coverage 低于 test-suite skill 的 85% 建议值；
本轮通过端到端二进制 UAT 补足行为验证，后续可继续增加故障注入和 CLI/TUI
表驱动测试。该缺口不阻塞当前功能验收。

## UAT 场景

`scripts/uat.ps1` 在临时数据根与 fake provider 上验证：

- A/B 各自 seq=1，calculator 互不串流；
- ReAct tool call 后继续模型回答；
- direct calculator/search/read_docs/todo/weather/memory；
- memory BM25+dense+rerank；
- quota_exhausted + reset；
- kill A Sessiond，B 继续，A generation 恢复；
- Hub 崩溃重启后 reattach 存活 Sessiond；
- graceful stop；
- 20 Session 对应 20 PID；
- 压缩、删除投影后 trace/stats 重建一致；
- account validate、positional/file/offline/fake smoke；
- startup install/run/status/uninstall；
- TUI 切换 session、slash 后 chat、再次切换后 chat；
- DeepSeek chat/tool_call 以 `deepseek-flash`、thinking enabled、high effort 调用 fake provider；
- Gateway 请求体包含 `model/messages/thinking/reasoning_effort/stream=false`，无默认 temperature；
- persisted/log 文件无测试凭据。

## 综合判断

✅ 本地功能与故障验收通过。  
⚠️ statement coverage 在进程/operator 包仍偏低；GitHub hosted CI 和真实 API
smoke 需在 commit/push、提供真实凭据后执行。
