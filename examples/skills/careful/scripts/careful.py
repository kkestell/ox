#!/usr/bin/env python3
"""The careful skill's hooks.

Ox runs this script for every hook the skill declares, with the hook input as
JSON on stdin. The input's `kind` selects the behavior. A result is printed as
one JSON object; a failure exits nonzero with the reason on stderr, which ends
the prompt run.
"""

import json
import re
import subprocess
import sys
from datetime import datetime, timezone

DESTRUCTIVE = re.compile(
    r"\brm\s+-[a-zA-Z]*[rf]"
    r"|\bgit\s+(push|reset\s+--hard|clean\s+-[a-zA-Z]*f)"
)

LOG = "runs.jsonl"


def git(workspace, *args):
    return subprocess.run(
        ["git", "-C", workspace, *args], capture_output=True, text=True
    )


def before_run(hook):
    status = git(hook["workspace"], "status", "--short", "--branch")
    if status.returncode != 0:
        sys.exit(f"git status failed: {status.stderr.strip()}")
    return {"message": "Git status before this run:\n" + status.stdout.strip()}


def before_tool(hook):
    if hook["tool"]["name"] != "shell":
        return {"decision": "allow"}
    try:
        command = json.loads(hook["tool"]["arguments"])["command"]
    except (ValueError, TypeError, KeyError):
        # Ox gives malformed arguments its normal failed tool result.
        return {"decision": "allow"}
    if isinstance(command, str) and DESTRUCTIVE.search(command):
        return {
            "decision": "deny",
            "message": f"`{command}` can destroy work. Use a reversible "
            "command, or ask the user.",
        }
    return {"decision": "allow"}


def after_tools(hook):
    if not any(tool["name"] == "apply_patch" for tool in hook["tools"]):
        return {}
    check = git(hook["workspace"], "diff", "--check")
    if check.returncode == 0:
        return {}
    if not check.stdout.strip():
        sys.exit(f"git diff --check failed: {check.stderr.strip()}")
    return {
        "message": "git diff --check found whitespace errors:\n"
        + check.stdout.strip()
    }


def after_run(hook):
    record = {
        "at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "session_id": hook["session_id"],
        "run_id": hook["run_id"],
        "arguments": hook["arguments"],
        "outcome": hook["outcome"],
        "error": hook["error"],
    }
    with open(LOG, "a") as log:
        log.write(json.dumps(record) + "\n")
    return {}


HOOKS = {
    "before_run": before_run,
    "before_tool": before_tool,
    "after_tools": after_tools,
    "after_run": after_run,
}


def main():
    hook = json.load(sys.stdin)
    print(json.dumps(HOOKS[hook["kind"]](hook)))


if __name__ == "__main__":
    main()
