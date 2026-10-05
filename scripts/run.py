#!/usr/bin/env python3
# Apply-patch stress prompt used for a live empty-workspace run:
#   Stress-test the apply_patch tool in this empty workspace. Use apply_patch for
#   every filesystem change; do not use shell redirection, sed, Python, or other
#   commands to write files. First, create a small fixture with README.md,
#   data/repeated.txt (three similar sections with repeated lines and Unicode),
#   src/config.txt (at least three named sections), old/location.txt, and
#   delete-me.txt. Then use a later single apply_patch call that mixes all of
#   these operations: make multiple separated updates to README.md and
#   src/config.txt; change only the middle similar section in data/repeated.txt;
#   move old/location.txt to archive/final.txt while changing its contents; add
#   nested/new.txt; and delete delete-me.txt. Use more than one update chunk for
#   at least one file. Finally, inspect the resulting tree and exact file
#   contents with read-only commands, confirm the old and deleted paths are
#   absent, and fix any discrepancy with apply_patch before reporting what you
#   verified.

import argparse
import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("prompt")
    parser.add_argument("--keep", action="store_true")
    parser.add_argument("--repo", metavar="GITHUB_COMMIT_URL")
    parser.add_argument("--model", metavar="MODEL_ID")
    parser.add_argument("--effort", metavar="LEVEL")
    args = parser.parse_args()

    repo = None
    if args.repo:
        match = re.fullmatch(
            r"https://github\.com/([^/]+)/([^/]+)/commit/([0-9a-fA-F]+)",
            args.repo.rstrip("/"),
        )
        if not match:
            parser.error("--repo must be a GitHub commit URL")
        owner, name, commit = match.groups()
        repo = f"https://github.com/{owner}/{name}.git"

    root = Path(__file__).resolve().parent.parent
    api_key = next(
        line.split("=", 1)[1]
        for line in (root / ".env").read_text().splitlines()
        if line.startswith("OPENROUTER_API_KEY=")
    )
    workspace = tempfile.mkdtemp(prefix="ox-")
    print(workspace, flush=True)
    try:
        if repo:
            subprocess.run(["git", "clone", repo, workspace], check=True)
            subprocess.run(["git", "checkout", commit], cwd=workspace, check=True)
        command = ["go", "run", "./cmd/ox-server", "run", "--dir", workspace]
        if args.model:
            command.extend(["--model", args.model])
        if args.effort:
            command.extend(["--effort", args.effort])
        command.append(args.prompt)
        return subprocess.run(
            command,
            env=os.environ | {"OPENROUTER_API_KEY": api_key},
            cwd=root,
        ).returncode
    finally:
        if not args.keep:
            shutil.rmtree(workspace)


raise SystemExit(main())
