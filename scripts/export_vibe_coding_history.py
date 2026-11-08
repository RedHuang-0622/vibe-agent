#!/usr/bin/env python
"""Export local Codex rollout JSONL into a sanitized vibe-coding history."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterable


THREADS = [
    {
        "id": "01a1a577-fc47-7000-804a-e636ce4b4863",
        "title": "设计 Go CLI Agent 架构与里程碑",
        "mode": "full",
    },
    {
        "id": "01a1a59a-4856-7e00-8d32-2bdb50265365",
        "title": "Build vibe_agent CLI runtime",
        "mode": "full",
    },
    {
        "id": "01a1a5d0-b787-7232-89a7-68214a45c8a7",
        "title": "实现本地 Agent CLI 运行时",
        "mode": "full",
    },
    {
        "id": "01a1bae6-c1d2-7d73-b219-5924e49486d5",
        "title": "对齐成熟架构并补冒烟测试",
        "mode": "truncate_before_api_key",
    },
]

SECRET_PATTERNS = [
    re.compile(r"sk-[A-Za-z0-9][A-Za-z0-9_-]{12,}"),
    re.compile(r"(?i)(Authorization\s*:\s*Bearer\s+)[^\s\"']+"),
    re.compile(r"(?i)(x-api-key\s*:\s*)[^\s\"']+"),
]
API_KEY_MESSAGE = re.compile(r"sk-[A-Za-z0-9][A-Za-z0-9_-]{12,}")

MARKDOWN_TYPES = {
    ("response_item", "message"),
    ("response_item", "function_call"),
    ("response_item", "function_call_output"),
    ("response_item", "custom_tool_call"),
    ("response_item", "custom_tool_call_output"),
    ("response_item", "tool_search_call"),
    ("response_item", "tool_search_output"),
    ("event_msg", "task_started"),
    ("event_msg", "task_complete"),
    ("event_msg", "turn_aborted"),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--sessions-root", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--overwrite", action="store_true")
    return parser.parse_args()


def sanitize(value: Any) -> Any:
    if isinstance(value, str):
        result = value
        for pattern in SECRET_PATTERNS:
            if pattern.groups:
                result = pattern.sub(r"\1[REDACTED]", result)
            else:
                result = pattern.sub("[REDACTED_API_KEY]", result)
        return result
    if isinstance(value, list):
        return [sanitize(item) for item in value]
    if isinstance(value, dict):
        return {key: sanitize(item) for key, item in value.items()}
    return value


def source_files(root: Path, thread_id: str) -> list[Path]:
    return sorted(root.rglob(f"rollout-*{thread_id}*.jsonl"))


def load_records(files: Iterable[Path]) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    for path in files:
        with path.open("r", encoding="utf-8") as handle:
            for line in handle:
                if not line.strip():
                    continue
                record = json.loads(line)
                record["_source_file"] = str(path)
                records.append(record)
    records.sort(key=lambda item: (int(item.get("ordinal", 0)), item["_source_file"]))
    return records


def message_text(payload: dict[str, Any]) -> str:
    parts: list[str] = []
    for item in payload.get("content", []):
        if not isinstance(item, dict):
            continue
        text = item.get("text")
        if text:
            parts.append(str(text))
        elif item.get("type") == "image":
            parts.append("[image omitted]")
    return "\n\n".join(parts)


def is_api_key_message(record: dict[str, Any]) -> bool:
    if record.get("type") != "response_item":
        return False
    payload = record.get("payload", {})
    if payload.get("type") != "message" or payload.get("role") != "user":
        return False
    return bool(API_KEY_MESSAGE.search(message_text(payload)))


def select_records(thread: dict[str, str], records: list[dict[str, Any]]) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    if thread["mode"] == "full":
        return records, {"mode": "full", "record_count": len(records)}
    selected: list[dict[str, Any]] = []
    cutoff: dict[str, Any] | None = None
    for record in records:
        if is_api_key_message(record):
            cutoff = {
                "mode": "truncate_before_api_key",
                "excluded_ordinal": int(record.get("ordinal", 0)),
                "excluded_timestamp": record.get("timestamp"),
                "reason": "first user message containing an sk- credential",
            }
            break
        selected.append(record)
    if cutoff is None:
        raise RuntimeError(f"credential cutoff message not found for {thread['id']}")
    cutoff["record_count"] = len(selected)
    return selected, cutoff


def markdown_fence(text: str) -> str:
    longest = 0
    for match in re.finditer(r"`+", text):
        longest = max(longest, len(match.group(0)))
    return "`" * max(3, longest + 1)


def render_record(record: dict[str, Any], counters: Counter[str]) -> str:
    timestamp = record.get("timestamp", "")
    ordinal = record.get("ordinal", "")
    record_type = record.get("type", "")
    payload = record.get("payload", {})
    payload_type = payload.get("type", "")
    counters[f"{record_type}:{payload_type}:{payload.get('role', '')}"] += 1

    if record_type == "response_item" and payload_type == "message":
        role = payload.get("role", "unknown")
        if role not in {"user", "assistant"}:
            return ""
        phase = f" ({payload['phase']})" if payload.get("phase") else ""
        return f"## {role.title()}{phase}\n\n_ordinal {ordinal} · {timestamp}_\n\n{message_text(payload)}\n"

    if payload_type in {"function_call", "custom_tool_call", "tool_search_call"}:
        name = payload.get("name", payload_type)
        call_id = payload.get("call_id", payload.get("id", ""))
        arguments = payload.get("arguments", payload.get("input", ""))
        if not isinstance(arguments, str):
            arguments = json.dumps(arguments, ensure_ascii=False, indent=2)
        fence = markdown_fence(arguments)
        return (
            f"## Tool Call: {name}\n\n"
            f"_ordinal {ordinal} · call_id `{call_id}` · {timestamp}_\n\n"
            f"{fence}json\n{arguments}\n{fence}\n"
        )

    if payload_type in {"function_call_output", "custom_tool_call_output", "tool_search_output"}:
        call_id = payload.get("call_id", payload.get("id", ""))
        output = payload.get("output", payload.get("result", ""))
        if not isinstance(output, str):
            output = json.dumps(output, ensure_ascii=False, indent=2)
        fence = markdown_fence(output)
        return (
            f"## Tool Output\n\n"
            f"_ordinal {ordinal} · call_id `{call_id}` · {timestamp}_\n\n"
            f"{fence}text\n{output}\n{fence}\n"
        )

    if record_type == "event_msg" and payload_type in {"task_started", "task_complete", "turn_aborted"}:
        status = payload.get("status", payload.get("reason", ""))
        return f"> {payload_type} · ordinal {ordinal} · {timestamp} · {status}\n"

    return ""


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def main() -> int:
    args = parse_args()
    sessions_root = Path(args.sessions_root).resolve()
    output = Path(args.output).resolve()
    if output.exists():
        if not args.overwrite:
            raise SystemExit(f"output already exists: {output}")
        if output.name != "vibecoding_history":
            raise SystemExit(f"refusing to replace unexpected output: {output}")
        for child in sorted(output.iterdir(), reverse=True):
            if child.is_dir():
                import shutil
                shutil.rmtree(child)
            else:
                child.unlink()
    else:
        output.mkdir(parents=True)

    threads_dir = output / "threads"
    threads_dir.mkdir()
    manifest_threads: list[dict[str, Any]] = []
    index_lines = [
        "# vibe_agent Vibe Coding History",
        "",
        "本目录由本地 Codex rollout JSONL 导出。当前导出线程只保留首个真实 API key 用户消息之前的记录；其余线程完整导出。",
        "",
        "| Thread | Mode | Records | Markdown | JSONL |",
        "|---|---|---:|---|---|",
    ]

    for thread in THREADS:
        files = source_files(sessions_root, thread["id"])
        if not files:
            raise RuntimeError(f"no rollout files found for {thread['id']}")
        records = load_records(files)
        selected, scope = select_records(thread, records)
        selected = [sanitize(record) for record in selected]

        thread_dir = threads_dir / thread["id"]
        thread_dir.mkdir()
        jsonl_path = thread_dir / "events.sanitized.jsonl"
        with jsonl_path.open("w", encoding="utf-8") as handle:
            for record in selected:
                handle.write(json.dumps(record, ensure_ascii=False, separators=(",", ":")) + "\n")

        counters: Counter[str] = Counter()
        header = [
            f"# {thread['title']}",
            "",
            f"- Thread ID: `{thread['id']}`",
            f"- Export mode: `{scope['mode']}`",
            f"- Selected records: `{len(selected)}`",
        ]
        if scope["mode"] != "full":
            header.append(
                f"- Cutoff: excluded ordinal `{scope['excluded_ordinal']}` and all later records"
            )
        header.extend([
            f"- Source files: {len(files)}",
            "",
            "---",
            "",
        ])
        body = [render_record(record, counters) for record in selected]
        transcript = "\n".join(header) + "\n".join(part for part in body if part)
        markdown_path = thread_dir / "transcript.md"
        markdown_path.write_text(transcript.rstrip() + "\n", encoding="utf-8")

        manifest_threads.append({
            "id": thread["id"],
            "title": thread["title"],
            "scope": scope,
            "source_files": [str(path) for path in files],
            "markdown": str(markdown_path.relative_to(output)).replace("\\", "/"),
            "jsonl": str(jsonl_path.relative_to(output)).replace("\\", "/"),
            "record_types": dict(sorted(counters.items())),
            "sha256": {
                "markdown": sha256(markdown_path),
                "jsonl": sha256(jsonl_path),
            },
        })
        index_lines.append(
            f"| [{thread['title']}](threads/{thread['id']}/transcript.md) | "
            f"{scope['mode']} | {len(selected)} | "
            f"[md](threads/{thread['id']}/transcript.md) | "
            f"[jsonl](threads/{thread['id']}/events.sanitized.jsonl) |"
        )

    index_path = output / "index.md"
    index_path.write_text("\n".join(index_lines) + "\n", encoding="utf-8")
    manifest = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "source_root": str(sessions_root),
        "secret_policy": "sanitized; sk- and authorization header values replaced",
        "threads": manifest_threads,
    }
    manifest_path = output / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    readme_path = output / "README.md"
    readme_path.write_text(
        "# Vibe Coding History\n\n"
        "本目录由 `scripts/export_vibe_coding_history.py` 从本地 Codex rollout JSONL 导出。\n\n"
        "- [按线程索引](index.md)\n"
        "- [Manifest](manifest.json)\n"
        "- [SHA256 校验](checksums.sha256)\n\n"
        "其他 3 个 `vibe_agent` 线程完整导出。当前线程仅保留首个真实 API key "
        "用户消息之前的记录；该消息及之后内容不进入导出。\n\n"
        "重新导出：\n\n"
        "```powershell\n"
        "python scripts/export_vibe_coding_history.py "
        "--sessions-root \"$env:USERPROFILE\\.codex\\sessions\" "
        "--output \"G:\\Project\\vibe_agent\\vibecoding_history\" --overwrite\n"
        "```\n",
        encoding="utf-8",
    )

    checksum_lines = []
    for path in sorted(output.rglob("*")):
        if path.is_file() and path.name != "checksums.sha256":
            checksum_lines.append(f"{sha256(path)}  {path.relative_to(output).as_posix()}")
    (output / "checksums.sha256").write_text("\n".join(checksum_lines) + "\n", encoding="utf-8")

    leaks = []
    for path in output.rglob("*"):
        if path.is_file():
            text = path.read_text(encoding="utf-8", errors="ignore")
            if API_KEY_MESSAGE.search(text):
                leaks.append(str(path))
    if leaks:
        raise RuntimeError(f"sanitized export still contains API-key-shaped text: {leaks}")

    print(output)
    print(index_path)
    print(manifest_path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
