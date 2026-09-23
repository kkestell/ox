#!/usr/bin/env python3
"""The goal skill's before_stop hook.

Reads the hook input from stdin, asks a headless `ox run` in the same
workspace to judge the answer against the objective, and prints the judge's
decision object. Invalid judge output or a failed run exits nonzero with the
reason on stderr.
"""

import json
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile

PROMPT = """You are judging whether another agent has met an objective in this workspace.

Objective:
{arguments}

The agent's latest answer:
{answer}

Inspect the workspace to check the answer against the objective. Run read-only
commands such as builds, tests, or file listings as needed. Do not change any
files.

Reply with only a JSON object and no other text:
{{"decision": "continue" or "stop", "message": "..."}}

Use "stop" when the objective is met, or when the agent cannot continue without
the user; the message says which, and what the user needs to provide. Use
"continue" when work remains; the message tells the agent specifically what is
still missing or wrong."""

FENCE = re.compile(r"```(?:json)?[ \t]*\n(.*?)\n?```", re.DOTALL)


def decision(output):
    """Returns the decision object in the judge's output, which may be
    wrapped in a code fence."""
    text = output.strip()
    fenced = FENCE.fullmatch(text)
    if fenced:
        text = fenced.group(1).strip()
    try:
        value = json.loads(text)
    except json.JSONDecodeError as error:
        raise ValueError(f"not JSON: {error}") from None
    if not isinstance(value, dict) or set(value) != {"decision", "message"}:
        raise ValueError('expected an object with only "decision" and "message"')
    if value["decision"] not in ("continue", "stop"):
        raise ValueError(f"unknown decision {value['decision']!r}")
    if not isinstance(value["message"], str) or not value["message"].strip():
        raise ValueError("message is blank")
    return value


def main():
    # Ox stops the whole process group, including the nested `ox run`. Waiting
    # for that run to finish its own cleanup lets this script delete its data
    # directory before exiting.
    signal.signal(signal.SIGTERM, lambda signum, frame: None)
    hook = json.load(sys.stdin)
    data_dir = tempfile.mkdtemp(prefix="ox-judge-")
    try:
        result = subprocess.run(
            [
                hook["ox"],
                "run",
                "--dir",
                hook["workspace"],
                "--model",
                hook["model"],
                "--effort",
                hook["effort"],
                PROMPT.format(arguments=hook["arguments"], answer=hook["answer"]),
            ],
            env=os.environ | {"OX_DATA_DIR": data_dir},
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
        )
    finally:
        shutil.rmtree(data_dir, ignore_errors=True)
    if result.returncode != 0:
        sys.exit(
            f"ox run failed with exit status {result.returncode}: "
            f"{result.stderr.strip()}"
        )
    try:
        value = decision(result.stdout)
    except ValueError as error:
        sys.exit(f"invalid judge output: {error}\n{result.stdout.strip()}")
    print(json.dumps(value))


if __name__ == "__main__":
    main()
