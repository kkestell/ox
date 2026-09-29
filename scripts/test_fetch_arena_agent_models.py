import json
import os
import tempfile
import unittest
from unittest import mock

import fetch_arena_agent_models as script


PARETO_MARKUP = """
<html><body>
<button title="Toggle Sidebar">menu</button>
<span title="Score: High to Low">sort</span>
<div class="w-full" aria-label="Cost per Task: 6 models, sorted by Ranking" role="figure">
  <div class="grid">
    <div class="row">
      <span class="rank"><span>1</span></span>
      <span class="logo"><svg><title>Anthropic</title><path d="M1 1"/></svg></span>
      <span class="name"><span title="Claude Opus 5 (Max)">Claude Opus 5 (Max)</span></span>
    </div>
    <div class="row">
      <span class="name"><span title="GPT 6 Sol (High)">GPT 6 Sol (High)</span></span>
    </div>
    <div class="row">
      <span class="name"><span title="Claude Opus 5 (High)">Claude Opus 5 (High)</span></span>
    </div>
    <div class="row">
      <span class="name"><span title="Mystery Model">Mystery Model</span></span>
    </div>
    <div class="row">
      <span class="name"><span title="Twin (xHigh)">Twin (xHigh)</span></span>
    </div>
    <div class="row">
      <span class="name"><span title="Kimi K3 (Max)">Kimi K3 (Max)</span></span>
    </div>
  </div>
</div>
<span title="After the figure">footer</span>
</body></html>
"""

PARETO_NAMES = [
    "Claude Opus 5 (Max)",
    "GPT 6 Sol (High)",
    "Claude Opus 5 (High)",
    "Mystery Model",
    "Twin (xHigh)",
    "Kimi K3 (Max)",
]

CATALOG = [
    {"id": "anthropic/claude-opus-5:free", "name": "Anthropic: Claude Opus 5 (free)"},
    {"id": "anthropic/claude-opus-5", "name": "Anthropic: Claude Opus 5"},
    {"id": "openai/gpt-6-sol", "name": "OpenAI: GPT 6 Sol"},
    {"id": "alpha/twin", "name": "Alpha: Twin"},
    {"id": "beta/twin", "name": "Beta: Twin"},
    {"id": "moonshotai/kimi-k3", "name": "MoonshotAI: Kimi K3"},
]

FRONTIER = ["anthropic/claude-opus-5", "openai/gpt-6-sol", "moonshotai/kimi-k3"]


class ParetoTests(unittest.TestCase):
    def test_pareto_models_are_the_figure_spans_in_source_order(self):
        self.assertEqual(script.pareto_models(PARETO_MARKUP), PARETO_NAMES)

    def test_frontier_ids_keep_order_and_drop_repeats_unmatched_and_ambiguous(self):
        self.assertEqual(script.frontier_ids(PARETO_NAMES, CATALOG), FRONTIER)

    def test_empty_pareto_list_and_empty_id_list_are_errors(self):
        with self.assertRaises(RuntimeError):
            script.pareto_models("<html><body>no figure</body></html>")
        with self.assertRaises(RuntimeError):
            script.frontier_ids(["Mystery Model", "Twin (xHigh)"], CATALOG)


class ConfigTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = os.path.join(self.directory.name, "config.json")

    def read(self):
        with open(self.path, encoding="utf-8") as file:
            return json.load(file)

    def test_update_config_replaces_frontier_and_keeps_servers(self):
        servers = [{"name": "Fake", "command": "fake", "args": []}]
        with open(self.path, "w", encoding="utf-8") as file:
            json.dump({"servers": servers, "frontier": ["old/model"]}, file)
        script.update_config(self.path, FRONTIER)
        self.assertEqual(self.read(), {"servers": servers, "frontier": FRONTIER})

    def test_update_config_creates_a_missing_file(self):
        script.update_config(self.path, FRONTIER)
        self.assertEqual(self.read(), {"frontier": FRONTIER})

    def test_fetch_and_parse_failures_leave_the_config_intact(self):
        original = {"servers": [], "frontier": ["old/model"]}
        with open(self.path, "w", encoding="utf-8") as file:
            json.dump(original, file)
        for name, fetch in [
            ("fetch failure", mock.Mock(side_effect=OSError("offline"))),
            ("empty page", mock.Mock(return_value="<html></html>")),
        ]:
            with self.subTest(name):
                with (
                    mock.patch.object(script, "fetch", fetch),
                    mock.patch("sys.argv", ["script", "--config", self.path]),
                    self.assertRaises((OSError, RuntimeError)),
                ):
                    script.main()
                self.assertEqual(self.read(), original)
        self.assertEqual(os.listdir(self.directory.name), ["config.json"])


if __name__ == "__main__":
    unittest.main()
