"""Tests judge.py against a fake `ox` executable. Run with
`python3 -m unittest discover -s examples/skills/goal/scripts`."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

JUDGE = Path(__file__).with_name("judge.py")

FAKE_OX = """#!/usr/bin/env python3
import json, os, sys
data_dir = os.environ["OX_DATA_DIR"]
with open(os.environ["FAKE_OX_RECORD"], "w") as record:
    json.dump({"args": sys.argv[1:], "data_dir": data_dir, "existed": os.path.isdir(data_dir)}, record)
sys.stdout.write(os.environ["FAKE_OX_STDOUT"])
sys.stderr.write(os.environ["FAKE_OX_STDERR"])
sys.exit(int(os.environ["FAKE_OX_STATUS"]))
"""


class JudgeTest(unittest.TestCase):
    def test_decisions_fences_invalid_output_and_failed_runs(self):
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            ox = directory / "ox"
            ox.write_text(FAKE_OX)
            ox.chmod(0o755)
            record = directory / "record.json"
            hook = {
                "skill": "goal",
                "arguments": "Make the parser tests pass.",
                "workspace": str(directory),
                "ox": str(ox),
                "model": "test/model",
                "effort": "medium",
                "answer": "The parser tests pass.",
            }
            continue_decision = {"decision": "continue", "message": "Two tests fail."}
            stop_decision = {"decision": "stop", "message": "Objective met."}
            cases = [
                (json.dumps(continue_decision) + "\n", "", 0, continue_decision, None),
                (
                    "```json\n" + json.dumps(stop_decision) + "\n```\n",
                    "",
                    0,
                    stop_decision,
                    None,
                ),
                ("I think it is done.\n", "", 0, None, "invalid judge output: not JSON"),
                (
                    '{"decision": "maybe", "message": "Unsure."}',
                    "",
                    0,
                    None,
                    "invalid judge output: unknown decision 'maybe'",
                ),
                (
                    "",
                    "no credentials",
                    2,
                    None,
                    "ox run failed with exit status 2: no credentials",
                ),
            ]
            for stdout, stderr, status, expected, error in cases:
                with self.subTest(stdout=stdout, status=status):
                    result = subprocess.run(
                        [sys.executable, str(JUDGE)],
                        input=json.dumps(hook),
                        capture_output=True,
                        text=True,
                        env=os.environ
                        | {
                            "FAKE_OX_RECORD": str(record),
                            "FAKE_OX_STDOUT": stdout,
                            "FAKE_OX_STDERR": stderr,
                            "FAKE_OX_STATUS": str(status),
                        },
                    )
                    if expected is None:
                        self.assertNotEqual(result.returncode, 0)
                        self.assertEqual(result.stdout, "")
                        self.assertIn(error, result.stderr)
                    else:
                        self.assertEqual(result.returncode, 0, result.stderr)
                        self.assertEqual(json.loads(result.stdout), expected)
                    run = json.loads(record.read_text())
                    self.assertEqual(
                        run["args"][:-1],
                        [
                            "run",
                            "--dir",
                            str(directory),
                            "--model",
                            "test/model",
                            "--effort",
                            "medium",
                        ],
                    )
                    prompt = run["args"][-1]
                    self.assertIn("Make the parser tests pass.", prompt)
                    self.assertIn("The parser tests pass.", prompt)
                    self.assertTrue(run["existed"])
                    self.assertFalse(os.path.exists(run["data_dir"]))


if __name__ == "__main__":
    unittest.main()
