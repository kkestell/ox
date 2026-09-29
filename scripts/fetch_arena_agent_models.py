#!/usr/bin/env python3
"""Match Arena Agent leaderboard models to OpenRouter catalog entries.

Without arguments, print a report of the ranking table. With `--config PATH`,
replace the `frontier` array in that Ox config file with the OpenRouter IDs of
the Pareto view's models, in the view's order.
"""

import argparse
import json
import os
import re
import tempfile
import unicodedata
from html.parser import HTMLParser
from urllib.request import Request, urlopen


ARENA_URL = "https://arena.ai/leaderboard/agent"
PARETO_URL = "https://arena.ai/leaderboard/agent/pareto"
OPENROUTER_URL = "https://openrouter.ai/api/v1/models"
EFFORT_LEVELS = {"none", "minimal", "low", "medium", "high", "xhigh", "max"}


def fetch(url):
    request = Request(url, headers={"User-Agent": "Mozilla/5.0"})
    with urlopen(request, timeout=30) as response:
        charset = response.headers.get_content_charset() or "utf-8"
        return response.read().decode(charset)


class ModelParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.models = []
        self.row = False
        self.cell_index = -1
        self.model_title = None

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "tr":
            self.row = True
            self.cell_index = -1
            self.model_title = None
        elif tag == "td" and self.row:
            self.cell_index += 1
        elif tag == "span" and self.row and self.cell_index == 1:
            self.model_title = attrs.get("title", self.model_title)

    def handle_endtag(self, tag):
        if tag == "tr" and self.row:
            if self.model_title:
                self.models.append(self.model_title)
            self.row = False


class ParetoParser(HTMLParser):
    """Collects the titled spans inside the Pareto view's figure element."""

    def __init__(self):
        super().__init__()
        self.models = []
        self.depth = 0

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "div":
            if self.depth:
                self.depth += 1
            elif attrs.get("role") == "figure":
                self.depth = 1
        elif tag == "span" and self.depth and attrs.get("title"):
            self.models.append(attrs["title"])

    def handle_endtag(self, tag):
        if tag == "div" and self.depth:
            self.depth -= 1


def pareto_models(html):
    parser = ParetoParser()
    parser.feed(html)
    if not parser.models:
        raise RuntimeError("No models found in the Agent Pareto view")
    return parser.models


def frontier_ids(arena_names, catalog):
    """The OpenRouter IDs of the uniquely matched names, in order, each once."""
    ids = []
    for arena_name in arena_names:
        matches = find_models(without_effort(arena_name), catalog)
        if len(matches) == 1 and matches[0] not in ids:
            ids.append(matches[0])
    if not ids:
        raise RuntimeError("No Pareto model matches an OpenRouter model")
    return ids


def update_config(path, frontier):
    """Replaces `frontier` in the JSON object at `path`, keeping other fields."""
    try:
        with open(path, encoding="utf-8") as file:
            config = json.load(file)
    except FileNotFoundError:
        config = {}
    config["frontier"] = frontier
    directory = os.path.dirname(os.path.abspath(path))
    descriptor, temporary = tempfile.mkstemp(dir=directory, suffix=".tmp")
    with os.fdopen(descriptor, "w", encoding="utf-8") as file:
        json.dump(config, file, indent=2)
        file.write("\n")
    os.replace(temporary, path)


def report():
    parser = ModelParser()
    parser.feed(fetch(ARENA_URL))
    if not parser.models:
        raise RuntimeError("No model rows found on the Agent leaderboard")

    catalog = json.loads(fetch(OPENROUTER_URL))["data"]
    print("Arena model\tOpenRouter ID\tEffort")
    for arena_name in parser.models:
        effort = effort_from_name(arena_name)
        model_name = without_effort(arena_name)
        matches = find_models(model_name, catalog)
        if len(matches) == 1:
            model_id = matches[0]
        elif matches:
            model_id = "AMBIGUOUS: " + ", ".join(matches)
        else:
            model_id = "NOT FOUND"
        print(f"{arena_name}\t{model_id}\t{effort or '—'}")


def main():
    arguments = argparse.ArgumentParser(description=__doc__)
    arguments.add_argument("--config", help="the Ox config file to update")
    config = arguments.parse_args().config
    if config is None:
        report()
        return
    names = pareto_models(fetch(PARETO_URL))
    catalog = json.loads(fetch(OPENROUTER_URL))["data"]
    frontier = frontier_ids(names, catalog)
    update_config(config, frontier)
    print(f"Wrote {len(frontier)} frontier models to {config}")


def effort_from_name(name):
    for value in re.findall(r"\(([^()]*)\)", name):
        effort = value.casefold()
        if effort in EFFORT_LEVELS:
            return effort
    return None


def without_effort(name):
    return re.sub(
        r"\s*\((?:none|minimal|low|medium|high|xhigh|max)\)",
        "",
        name,
        flags=re.IGNORECASE,
    ).strip()


def name_key(name):
    return "".join(
        char
        for char in unicodedata.normalize("NFKD", name).casefold()
        if char.isalnum()
    )


def find_models(arena_name, catalog):
    key = name_key(arena_name)
    matches = []
    for model in catalog:
        model_id = model["id"]
        if ":" in model_id:
            continue

        display_name = model.get("name", "").split(":", 1)[-1].strip()
        display_name = re.sub(r"\s*\(\d+\)$", "", display_name)
        slug = model_id.rsplit("/", 1)[-1]
        if key in {name_key(display_name), name_key(slug)}:
            matches.append(model_id)
    return matches


if __name__ == "__main__":
    main()
