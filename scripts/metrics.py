#!/usr/bin/env python3
"""Print size, cognitive complexity, and lint suppressions of the files staged
for commit, as markdown.

Needs cloc, Go, and the web client's node_modules.
"""

import json
import re
import subprocess
import sys
import tempfile
from pathlib import Path

COMPLEXITY_THRESHOLD = 15
FILE_THRESHOLD = 500
GOCOGNIT = "github.com/uudashr/gocognit/cmd/gocognit@v1.3.0"
WEB = "cmd/ox/web"
LANGUAGES = ("Go", "TypeScript", "CSS")
SUPPRESSION = {"Go": "nolint", "TypeScript": "biome-ignore", "CSS": "biome-ignore"}
ROOT = Path(__file__).resolve().parent.parent


def run(*args, cwd=ROOT, input=None, check=True):
    result = subprocess.run(args, cwd=cwd, input=input, capture_output=True, text=True)
    if check and result.returncode != 0:
        sys.exit(f"{' '.join(map(str, args))} failed:\n{result.stderr}")
    return result.stdout


def classify(path, nested_modules):
    """Return a measured file's (language, is_test), or None. Go files in
    modules nested below the root module belong to those modules."""
    if path.endswith(".go"):
        if any(path.startswith(module) for module in nested_modules):
            return None
        return "Go", path.endswith("_test.go")
    if not path.startswith((f"{WEB}/src/", f"{WEB}/e2e/")):
        return None
    if path.endswith((".ts", ".tsx")):
        language = "TypeScript"
    elif path.endswith(".css"):
        language = "CSS"
    else:
        return None
    return language, path.startswith(f"{WEB}/e2e/")


def go_complexity(snapshot, files):
    out = run("go", "run", GOCOGNIT, "-json", "-test=false", *(snapshot / f for f in files))
    return [
        (f["Complexity"], f["FuncName"], f"{Path(f['Pos']['Filename']).relative_to(snapshot)}:{f['Pos']['Line']}")
        for f in json.loads(out) or []
    ]


def ts_name(line, column):
    """Name the function Biome reports at a column: its identifier, or the
    variable or property an arrow function is assigned to."""
    named = re.match(r"\w+", line[column - 1:])
    if named:
        return named.group()
    assigned = re.match(r"\s*(?:export\s+)?(?:const\s+|let\s+)?([\w.]+)\s*=", line)
    if assigned:
        return assigned.group(1)
    return "(anonymous)"


def ts_complexity(snapshot):
    with tempfile.TemporaryDirectory() as config:
        rule = {"level": "error", "options": {"maxAllowedComplexity": COMPLEXITY_THRESHOLD}}
        Path(config, "biome.json").write_text(json.dumps({
            "linter": {"rules": {"preset": "none", "complexity": {"noExcessiveCognitiveComplexity": rule}}},
        }))
        out = run(
            ROOT / WEB / "node_modules/.bin/biome", "lint", f"--config-path={config}",
            "--reporter=json", "--max-diagnostics=none", "src",
            cwd=snapshot / WEB, check=False,
        )
    found = []
    for d in json.loads(out)["diagnostics"]:
        if d["category"] != "lint/complexity/noExcessiveCognitiveComplexity":
            continue
        complexity = int(re.search(r"complexity of (\d+)", d["message"]).group(1))
        path = f"{WEB}/{d['location']['path']}"
        start = d["location"]["start"]
        line = (snapshot / path).read_text().splitlines()[start["line"] - 1]
        found.append((complexity, ts_name(line, start["column"]), f"{path}:{start['line']}"))
    return found


def table(header, rows):
    if not rows:
        return "None.\n"
    rows = [[str(c) for c in row] for row in rows]
    numeric = [all(re.fullmatch(r"[\d,]+", row[i]) for row in rows) for i in range(len(header))]
    lines = ["| " + " | ".join(header) + " |", "|" + "|".join("---:" if n else "---" for n in numeric) + "|"]
    lines += ["| " + " | ".join(row) + " |" for row in rows]
    return "\n".join(lines) + "\n"


def main():
    with tempfile.TemporaryDirectory() as tmp:
        snapshot = Path(tmp)
        # Measure the index, so the numbers match the commit even when the
        # working tree has other changes.
        run("git", "checkout-index", "--all", f"--prefix={snapshot}/")
        nested_modules = [
            str(Path(p).parent) + "/" for p in run("git", "ls-files", "*/go.mod").splitlines()
        ]
        measured = {p: c for p in run("git", "ls-files").splitlines() if (c := classify(p, nested_modules))}

        by_file = json.loads(run("cloc", "--quiet", "--json", "--by-file", "--skip-uniqueness", "--list-file=-",
                                 cwd=snapshot, input="\n".join(measured)))
        size = {(language, test): {"files": 0, "comment": 0, "code": 0}
                for language in LANGUAGES for test in (False, True)}
        suppressions = dict.fromkeys(LANGUAGES, 0)
        for path, (language, test) in measured.items():
            group = size[language, test]
            group["files"] += 1
            group["comment"] += by_file[path]["comment"]
            group["code"] += by_file[path]["code"]
            suppressions[language] += (snapshot / path).read_text().count(SUPPRESSION[language])

        go_files = [p for p, (language, test) in measured.items() if language == "Go" and not test]
        functions = [
            f for f in go_complexity(snapshot, go_files) + ts_complexity(snapshot) if f[0] > COMPLEXITY_THRESHOLD
        ]

    size_rows = [
        (language, f"{size[language, False]['files']:,}", f"{size[language, False]['comment']:,}",
         f"{size[language, False]['code']:,}", f"{size[language, True]['code']:,}")
        for language in LANGUAGES
    ]
    complexity_rows = [
        (complexity, f"`{name}`", f"`{location}`")
        for complexity, name, location in sorted(functions, key=lambda f: (-f[0], f[2]))
    ]
    large_rows = [
        (f"{by_file[p]['code']:,}", f"`{p}`")
        for p in sorted(measured, key=lambda p: (-by_file[p]["code"], p))
        if not measured[p][1] and by_file[p]["code"] > FILE_THRESHOLD
    ]
    suppression_rows = [(language, f"`{SUPPRESSION[language]}`", suppressions[language]) for language in LANGUAGES]

    markdown = (
        "# Metrics\n\n"
        "Printed by `make metrics` from the files in the index.\n\n"
        "## Size\n\n"
        "Lines counted by cloc. Files, Comment, and Code cover production files; Tests is the code in "
        f"`*_test.go` and `{WEB}/e2e`.\n\n"
        + table(("Language", "Files", "Comment", "Code", "Tests"), size_rows)
        + "\n## High complexity\n\n"
        f"Production functions with cognitive complexity over {COMPLEXITY_THRESHOLD}, "
        "scored by gocognit for Go and Biome for TypeScript.\n\n"
        + table(("Complexity", "Function", "Location"), complexity_rows)
        + "\n## Long files\n\n"
        f"Production files with over {FILE_THRESHOLD} lines of code.\n\n"
        + table(("Code", "File"), large_rows)
        + "\n## Lint suppressions\n\n"
        "Comments that silence a linter, in production and test files.\n\n"
        + table(("Language", "Comment", "Suppressions"), suppression_rows)
    )
    print(markdown, end="")


if __name__ == "__main__":
    main()
