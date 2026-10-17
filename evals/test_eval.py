import concurrent.futures
import json
import sqlite3
import subprocess
import tempfile
import threading
import unittest
import xml.etree.ElementTree as ET
from pathlib import Path
from unittest.mock import patch

import eval as runner


class EvaluationTests(unittest.TestCase):
    def metrics(self, entries):
        with tempfile.TemporaryDirectory() as directory:
            database = Path(directory) / "ox.db"
            with sqlite3.connect(database) as connection:
                connection.execute(
                    "CREATE TABLE transcript_entries "
                    "(id INTEGER PRIMARY KEY, session_id TEXT, kind TEXT, data TEXT)"
                )
                connection.executemany(
                    "INSERT INTO transcript_entries (session_id, kind, data) VALUES (?, ?, ?)",
                    [("session", kind, json.dumps(data)) for kind, data in entries],
                )
            return runner.transcript_metrics(database)

    @staticmethod
    def batch(usage):
        return "assistant_batch", {
            "message": {"tool_calls": None, "usage": usage}, "outcomes": None
        }

    def test_final_response_without_tools_is_counted(self):
        usage = dict(input_tokens=10, cached_tokens=2, output_tokens=5,
                     reasoning_tokens=1, cost=0.25)
        metrics = self.metrics([self.batch(usage)])
        self.assertEqual(metrics["requests"], 1)
        self.assertEqual(metrics["tool_calls"], 0)
        self.assertEqual(metrics["input_tokens"], 10)
        self.assertEqual(metrics["cost"], 0.25)

    def test_missing_usage_makes_total_cost_unavailable(self):
        usage = dict(input_tokens=10, cached_tokens=0, output_tokens=5,
                     reasoning_tokens=0, cost=0.25)
        for missing in [self.batch(None), ("compaction", {"usage": None})]:
            for entries in [[missing, self.batch(usage)], [self.batch(usage), missing]]:
                with self.subTest(entries=entries):
                    metrics = self.metrics(entries)
                    self.assertEqual(metrics["requests"], 2)
                    self.assertEqual(metrics["input_tokens"], 10)
                    self.assertIsNone(metrics["cost"])

    def test_chart_accepts_repetitions_with_zero_metrics(self):
        for base, candidate in [([0, 1, 2], [1, 2, 3]), ([1, 2, 3], [0, 0, 0])]:
            results = {
                label: [
                    dict(task="task", model="model", passed=False,
                         **{key: value for key, _ in runner.CHART_METRICS})
                    for value in values
                ]
                for label, values in [("base", base), ("change", candidate)]
            }
            with self.subTest(base=base, candidate=candidate):
                svg = runner.chart_svg(results, "base", "change")
                ET.fromstring(svg)
                if candidate == [0, 0, 0]:
                    self.assertIn("-100%", svg)

    def test_parallel_repetitions_wait_for_the_mirror_clone(self):
        cloning, release, second_started = (threading.Event() for _ in range(3))
        task = {"repository": "https://example.invalid/compiler.git", "commit": "abc"}

        def clone(*args):
            Path(args[-1]).mkdir()
            cloning.set()
            if not release.wait(5):
                raise TimeoutError("clone was not released")

        def second():
            second_started.set()
            return runner.mirror(task)

        with tempfile.TemporaryDirectory() as directory, patch.object(
            runner, "REPOS", Path(directory)
        ), patch.object(runner, "git", side_effect=clone) as git, patch.object(
            runner.subprocess, "run", return_value=subprocess.CompletedProcess([], 0)
        ):
            with concurrent.futures.ThreadPoolExecutor(2) as pool:
                first = pool.submit(runner.mirror, task)
                try:
                    self.assertTrue(cloning.wait(5))
                    other = pool.submit(second)
                    self.assertTrue(second_started.wait(5))
                    with self.assertRaises(concurrent.futures.TimeoutError):
                        other.result(timeout=0.1)
                finally:
                    release.set()
                self.assertEqual(first.result(timeout=5), other.result(timeout=5))
            git.assert_called_once()


if __name__ == "__main__":
    unittest.main()
