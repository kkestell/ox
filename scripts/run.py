#!/usr/bin/env python3
import argparse
import os
import shutil
import subprocess
import tempfile
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("prompt")
    parser.add_argument("--keep", action="store_true")
    args = parser.parse_args()

    api_key = next(
        line.split("=", 1)[1]
        for line in Path(".env").read_text().splitlines()
        if line.startswith("OPENROUTER_API_KEY=")
    )
    workspace = tempfile.mkdtemp(prefix="ox-")
    print(workspace, flush=True)
    try:
        return subprocess.run(
            ["cargo", "run", "--", "run", "--dir", workspace, args.prompt],
            env=os.environ | {"OPENROUTER_API_KEY": api_key},
        ).returncode
    finally:
        if not args.keep:
            shutil.rmtree(workspace)


raise SystemExit(main())
