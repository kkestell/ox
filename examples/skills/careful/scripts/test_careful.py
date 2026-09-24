"""Tests careful.py in a temporary Git repository. Run with
`python3 -m unittest discover -s examples/skills/careful/scripts`."""

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

CAREFUL = Path(__file__).with_name("careful.py")


class CarefulTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.workspace = self.directory / "workspace"
        self.workspace.mkdir()
        for args in (
            ["init", "--quiet", "--initial-branch=main"],
            ["config", "user.email", "ox@example.com"],
            ["config", "user.name", "Ox"],
        ):
            subprocess.run(["git", "-C", self.workspace, *args], check=True)
        (self.workspace / "notes.txt").write_text("one\n")
        subprocess.run(["git", "-C", self.workspace, "add", "."], check=True)
        subprocess.run(
            ["git", "-C", self.workspace, "commit", "--quiet", "-m", "Start"],
            check=True,
        )

    def run_hook(self, kind, workspace=None, **fields):
        hook = {
            "kind": kind,
            "skill": "careful",
            "arguments": "Tidy the notes.",
            "session_id": "session-1",
            "mode": "ask",
            "run_id": "run-1",
            "workspace": str(workspace or self.workspace),
            "ox": "/unused/ox",
            "model": "test/model",
            "effort": "medium",
            **fields,
        }
        return subprocess.run(
            [sys.executable, str(CAREFUL)],
            input=json.dumps(hook),
            capture_output=True,
            text=True,
            cwd=self.directory,
        )

    def output(self, kind, **fields):
        result = self.run_hook(kind, **fields)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_before_run_reports_the_git_status(self):
        (self.workspace / "notes.txt").write_text("two\n")
        message = self.output("before_run")["message"]
        self.assertIn("## main", message)
        self.assertIn(" M notes.txt", message)

    def test_before_tool_denies_destructive_shell_text(self):
        def start(text, **extra):
            return ("shell", {"command": text, **extra})

        def write(text):
            return (
                "shell_process",
                {"action": "write", "process_id": "p-1", "text": text},
            )

        for text, decision in [
            ("rm -rf build", "deny"),
            ("git reset --hard HEAD", "deny"),
            ("git push origin main", "deny"),
            ("ls -la", "allow"),
            ("git status", "allow"),
        ]:
            for name, arguments in [
                start(text),
                start(text, background=True),
                write(text + "\n"),
            ]:
                with self.subTest(name=name, arguments=arguments):
                    tool = {
                        "call_id": "call-1",
                        "name": name,
                        "arguments": json.dumps(arguments),
                    }
                    output = self.output("before_tool", tool=tool)
                    self.assertEqual(output["decision"], decision)
                    self.assertEqual("message" in output, decision == "deny")
        for name, arguments in [
            ("shell", '{"comm'),
            ("shell_process", '{"action": "write"'),
            ("shell_process", json.dumps({"action": "write", "process_id": "p-1"})),
            ("shell_process", json.dumps(["write", "rm -rf build"])),
            ("shell_process", json.dumps({"action": "list"})),
            ("shell_process", json.dumps({"action": "stop", "process_id": "rm -rf build"})),
            ("apply_patch", "rm -rf build"),
        ]:
            with self.subTest(name=name, arguments=arguments):
                tool = {"call_id": "call-2", "name": name, "arguments": arguments}
                self.assertEqual(self.output("before_tool", tool=tool), {"decision": "allow"})

    def test_after_tools_reports_whitespace_errors_and_fails_outside_git(self):
        shell = {"call_id": "call-1", "name": "shell"}
        patch = {"call_id": "call-2", "name": "apply_patch"}
        self.assertEqual(self.output("after_tools", tools=[shell]), {})
        (self.workspace / "notes.txt").write_text("one \n")
        self.assertEqual(self.output("after_tools", tools=[shell]), {})
        message = self.output("after_tools", tools=[shell, patch])["message"]
        self.assertIn("notes.txt:1: trailing whitespace.", message)
        outside = self.directory / "outside"
        outside.mkdir()
        result = self.run_hook("after_tools", workspace=outside, tools=[patch])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertIn("git diff --check failed", result.stderr)

    def test_after_run_records_the_outcome(self):
        for outcome, error in [("finished", None), ("failed", "the model request failed")]:
            self.assertEqual(
                self.output("after_run", outcome=outcome, answer=None, error=error), {}
            )
        records = [
            json.loads(line)
            for line in (self.directory / "runs.jsonl").read_text().splitlines()
        ]
        self.assertEqual(
            [(record["outcome"], record["error"]) for record in records],
            [("finished", None), ("failed", "the model request failed")],
        )
        self.assertEqual(records[0]["run_id"], "run-1")


if __name__ == "__main__":
    unittest.main()
