#!/usr/bin/env python
"""Run the line-oriented TUI behind a hidden Windows pseudo terminal."""

import argparse
import json
import os
import threading
import time

from winpty import PtyProcess


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    args = parser.parse_args()
    with open(args.config, "r", encoding="utf-8") as handle:
        config = json.load(handle)

    output_path = config["output"]
    deadline = time.monotonic() + float(config.get("timeout_seconds", 180))
    process = PtyProcess.spawn(
        [config["exe"]],
        cwd=config["cwd"],
        env=dict(os.environ),
        dimensions=(40, 160),
    )
    output = bytearray()
    lock = threading.Lock()

    def reader() -> None:
        while True:
            try:
                chunk = process.read(4096)
            except EOFError:
                return
            if not chunk:
                return
            encoded = chunk.encode("utf-8", errors="replace")
            with lock:
                output.extend(encoded)
                with open(output_path, "wb") as handle:
                    handle.write(output)
                    handle.flush()

    thread = threading.Thread(target=reader, daemon=True)
    thread.start()
    time.sleep(0.5)
    commands = [
        f"/use {config['session_id']}",
        config["prompt"],
        "/board",
        "/exit",
    ]
    for command in commands:
        process.write(command + "\r\n")
        time.sleep(0.25)

    timed_out = False
    while process.isalive() and time.monotonic() < deadline:
        time.sleep(0.1)
    if process.isalive():
        timed_out = True
        process.terminate(force=True)
    try:
        process.wait()
    except Exception:
        pass
    thread.join(timeout=2)
    with open(output_path, "wb") as handle:
        with lock:
            handle.write(output)
    if timed_out:
        print("PTY probe timed out", flush=True)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
