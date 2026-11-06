#!/usr/bin/env python3
"""Deterministic CLI transport; the proof-checking subprocess is real."""
import json
import fcntl
import os
import pathlib
import subprocess
import sys
import time
import uuid


def emit(value):
    print(json.dumps(value), flush=True)


def verify():
    gate = pathlib.Path(os.environ["PROOFBRIDGE_FIXTURE_GATE"])
    deadline = time.monotonic() + 20
    while not gate.exists():
        if time.monotonic() >= deadline:
            raise RuntimeError("test observer did not release proof gate")
        time.sleep(0.02)
    # Shared candidate/verifier workspace: serialize compiler output files.
    with open(os.environ["PROOFBRIDGE_FIXTURE_LOG"] + ".lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        result = subprocess.run([os.environ["PROOFBRIDGE_FIXTURE_COQC"], "Goal.v"],
                                capture_output=True, text=True, timeout=15)
    with open(os.environ["PROOFBRIDGE_FIXTURE_LOG"], "a", encoding="utf-8") as log:
        log.write(json.dumps({"cwd": os.getcwd(), "exitCode": result.returncode,
                              "managedWorker": os.environ.get("PROOFBRIDGE_MANAGED_WORKER"),
                              "transport": "app-server" if "app-server" in sys.argv else "exec"}) + "\n")
    return result


if "--version" in sys.argv:
    print("codex-cli 0.162.1 (deterministic ProofBridge test fixture)")
    sys.exit(0)

if "exec" in sys.argv:
    sys.stdin.read()
    checked = verify()
    status = "pass" if checked.returncode == 0 else "continue"
    reason = "coqc Goal.v exited " + str(checked.returncode)
    verdict = {"status": status, "reason": reason,
               "checks": [{"name": name, "status": status, "reason": reason}
                          for name in ["task", "workspace", "verification"]]}
    emit({"type": "thread.started", "thread_id": "fixture-verifier-" + uuid.uuid4().hex})
    emit({"type": "item.completed", "item": {"type": "agent_message", "text": json.dumps(verdict)}})
    emit({"type": "turn.completed", "usage": {"input_tokens": 0, "output_tokens": 0}})
    sys.exit(0)

thread = "fixture-" + uuid.uuid4().hex
for line in sys.stdin:
    message = json.loads(line)
    method = message.get("method")
    if method == "initialize":
        emit({"id": message["id"], "result": {"userAgent": "proofbridge-deterministic-fixture"}})
    elif method in ("thread/start", "thread/resume"):
        thread = message.get("params", {}).get("threadId", thread)
        emit({"id": message["id"], "result": {"thread": {"id": thread}, "model": "deterministic-cli-fixture"}})
    elif method == "thread/unsubscribe":
        emit({"id": message["id"], "result": {"status": "unsubscribed"}})
    elif method == "turn/start":
        turn = "turn-" + uuid.uuid4().hex
        emit({"id": message["id"], "result": {"turn": {"id": turn, "status": "inProgress"}}})
        checked = verify()
        emit({"method": "item/completed", "params": {"threadId": thread, "turnId": turn,
              "item": {"id": "coqc-" + turn, "type": "commandExecution", "command": "coqc Goal.v",
                       "status": "completed", "exitCode": checked.returncode,
                       "aggregatedOutput": checked.stdout + checked.stderr}}})
        state = "resolved" if checked.returncode == 0 else "needs_next"
        remaining = "none" if checked.returncode == 0 else "fix the Coq compiler error"
        text = ("Real coqc Goal.v exited " + str(checked.returncode) +
                "\nMsg: to=user; intent=final; need=none\nHandoff: status=" + state +
                "; changed=none; verified=coqc Goal.v; next=" + remaining + "; risks=none")
        emit({"method": "item/completed", "params": {"threadId": thread, "turnId": turn,
              "item": {"id": "result-" + turn, "type": "agentMessage", "text": text}}})
        emit({"method": "turn/completed", "params": {"threadId": thread,
              "turn": {"id": turn, "status": "completed", "items": [], "error": None}}})
    elif message.get("id") is not None and method:
        emit({"id": message["id"], "result": {}})
