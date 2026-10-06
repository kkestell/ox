#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["pyyaml"]
# ///
# Runs the tasks in evals/tasks.yaml against one build of ox-server, each
# repetition in its own Docker container, and compares labeled runs. A run ends
# when Ox finishes, fails, or times out, and is then graded as tasks.yaml
# describes.
#
#   evals/eval.py run --label base --ref main --model MODEL_ID --effort LEVEL
#   evals/eval.py run --label change --ref BRANCH --model MODEL_ID --effort LEVEL
#   evals/eval.py compare base change    # writes evals/reports/base-vs-change.md
#   evals/eval.py validate               # grades each task's commit and parent
#
# Without --ref, `run` builds the working tree. Running an existing label again
# runs only its repetitions without a result. Models use OPENROUTER_API_KEY
# from .env.

import argparse
import concurrent.futures
import datetime
import json
import math
import os
import random
import re
import shutil
import sqlite3
import statistics
import subprocess
import tempfile
import textwrap
import time
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
EVALS = ROOT / "evals"
REPOS = EVALS / "repos"
REPORTS = EVALS / "reports"
TASKS = EVALS / "tasks.yaml"
IMAGE = "ox-eval"
# Seconds between SIGTERM and SIGKILL for a timed-out run.
KILL_GRACE = 30
# Seconds each grading step may take.
GRADE_TIMEOUT = 600

METRICS = [
    "status",
    "passed",
    "seconds",
    "requests",
    "tool_calls",
    "failed_calls",
    "cancelled_calls",
    "calls_by_tool",
    "repeated_calls",
    "input_tokens",
    "cached_tokens",
    "output_tokens",
    "reasoning_tokens",
    "cost",
    "max_input_tokens",
    "tool_output_chars",
]
# The chart's metrics, where lower is better, and the ratios its axis may mark.
CHART_METRICS = [
    ("cost", "Cost"),
    ("seconds", "Time"),
    ("input_tokens", "Input tokens"),
    ("output_tokens", "Output tokens"),
    ("requests", "Requests"),
]
CHART_TICKS = [0.1, 0.2, 0.25, 1 / 3, 0.5, 2 / 3, 0.8, 0.9, 1]
CHART_TICKS += [1.1, 1.25, 1.5, 2, 2.5, 3, 4, 5, 10]
BOOTSTRAP_SAMPLES = 2000


def main():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    run_parser = commands.add_parser("run")
    run_parser.add_argument("--label", required=True)
    run_parser.add_argument("--model", required=True, metavar="MODEL_ID")
    run_parser.add_argument("--effort", required=True, metavar="LEVEL")
    run_parser.add_argument("--ref", metavar="GIT_REF")
    run_parser.add_argument("--reps", type=int, default=3)
    run_parser.add_argument("--jobs", type=int, default=4)
    run_parser.add_argument("--timeout", type=int, default=600, metavar="SECONDS")
    run_parser.add_argument("--task", nargs="+", dest="tasks", metavar="ID")
    compare_parser = commands.add_parser("compare")
    compare_parser.add_argument("labels", nargs="+", metavar="LABEL")
    validate_parser = commands.add_parser("validate")
    validate_parser.add_argument("--jobs", type=int, default=4)
    validate_parser.add_argument("--task", nargs="+", dest="tasks", metavar="ID")
    args = parser.parse_args()
    if args.command == "run":
        run(parser, args)
    elif args.command == "validate":
        return validate(parser, args)
    else:
        compare(parser, args.labels)


def load_tasks(parser, ids):
    tasks = yaml.safe_load(TASKS.read_text())
    if ids:
        unknown = set(ids) - {task["id"] for task in tasks}
        if unknown:
            parser.error(f"unknown tasks: {', '.join(sorted(unknown))}")
        tasks = [task for task in tasks if task["id"] in ids]
    return tasks


def run(parser, args):
    if not re.fullmatch(r"[A-Za-z0-9._-]+", args.label):
        parser.error("--label may contain only letters, digits, '.', '_', and '-'")
    label_dir = EVALS / "runs" / args.label
    tasks = load_tasks(parser, args.tasks)
    binary, commit, dirty = build(args.ref, args.label)
    api_key = next(
        line.split("=", 1)[1]
        for line in (ROOT / ".env").read_text().splitlines()
        if line.startswith("OPENROUTER_API_KEY=")
    )
    identity = {
        "label": args.label,
        "commit": commit,
        "dirty": dirty,
        "model": args.model,
        "effort": args.effort,
        "providers": pinned_providers(args.model),
    }
    for saved_path in label_dir.glob("*/*/result.json"):
        saved = json.loads(saved_path.read_text())
        if any(
            saved[key] != identity[key]
            for key in ("commit", "dirty", "model", "effort", "providers")
        ):
            parser.error(
                f"{label_dir} holds results for a different commit, model, effort,"
                " or providers"
            )
    runs = [
        (task, rep)
        for rep in range(1, args.reps + 1)
        for task in tasks
        if not (label_dir / task["id"] / str(rep) / "result.json").exists()
    ]
    with concurrent.futures.ThreadPoolExecutor(args.jobs) as pool:
        futures = [
            pool.submit(run_repetition, binary, api_key, args, identity, task, rep)
            for task, rep in runs
        ]
        try:
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                outcome = "passed" if result["passed"] else f"failed ({result['failure']})"
                print(
                    f"{result['task']} {result['rep']}: {result['status']}, {outcome},"
                    f" {result['requests']} requests,"
                    f" {result['tool_calls']} tool calls,"
                    f" {number('cost', result['cost'])}, {result['seconds']:.0f}s",
                    flush=True,
                )
        except KeyboardInterrupt:
            # Removing the containers ends the running `docker exec` calls, so
            # the pool's threads finish instead of waiting on `sleep infinity`.
            pool.shutdown(wait=False, cancel_futures=True)
            remove_containers(args.label)
            raise SystemExit(130)


def pinned_providers(model):
    """The providers pinned for the model in the global settings file, or []."""
    path = Path.home() / ".config" / "ox" / "settings.json"
    if not path.exists():
        return []
    models = json.loads(path.read_text()).get("models", {})
    return models.get(model, {}).get("providers", [])


def git(*args, cwd=ROOT):
    return subprocess.run(
        ["git", *args], cwd=cwd, check=True, capture_output=True, text=True
    ).stdout.strip()


def mirror(task):
    """A mirror clone of the task's repository that holds its commit."""
    path = REPOS / Path(task["repository"]).name
    if not path.exists():
        REPOS.mkdir(parents=True, exist_ok=True)
        git("clone", "--mirror", "--quiet", task["repository"], str(path))
    elif subprocess.run(
        ["git", "cat-file", "-e", f"{task['commit']}^{{commit}}"], cwd=path
    ).returncode:
        git("fetch", "--quiet", cwd=path)
    return path


def extract(container, task, ref):
    """Writes the files of the task's ref to the container's /workspace."""
    archive = subprocess.run(
        ["git", "archive", ref], cwd=mirror(task), check=True, capture_output=True
    ).stdout
    subprocess.run(
        ["docker", "exec", "-i", container, "tar", "-x", "-C", "/workspace"],
        input=archive,
        check=True,
        capture_output=True,
    )


def grade(container, task):
    """Builds and tests the compiler in the container's /workspace, then compiles
    and runs the task's program. Returns None when the program exits 0 and
    prints the task's output, and otherwise why the run failed."""
    with tempfile.TemporaryDirectory() as directory:
        for name, source in task["program"].items():
            (Path(directory) / name).write_text(source)
        docker("cp", f"{directory}/.", f"{container}:/program")
    steps = [
        ("make", "/workspace", ["make"]),
        ("make test", "/workspace", ["make", "test"]),
        (
            "compiling the program",
            "/workspace",
            ["target/release/oberon", "-o", "/program/main", "/program/Main.Mod"],
        ),
        ("the program", "/program", ["/program/main"]),
    ]
    for name, directory, command in steps:
        process = subprocess.run(
            ["docker", "exec", "-w", directory, container, "timeout"]
            + ["--signal=KILL", str(GRADE_TIMEOUT), *command],
            stdin=subprocess.DEVNULL,
            capture_output=True,
        )
        if process.returncode == 137:
            return f"{name} timed out"
        if process.returncode:
            return (
                f"{name} exited {process.returncode}:"
                f" {(process.stdout + process.stderr)[-300:].decode(errors='replace')}"
            )
    if process.stdout.decode(errors="replace") != task["output"]:
        return f"the program printed {process.stdout[-300:].decode(errors='replace')!r}"
    return None


def validate(parser, args):
    """Checks that each task's program passes with its commit and fails with
    the commit's first parent, so the program tests the commit's change."""
    tasks = load_tasks(parser, args.tasks)
    subprocess.run(["docker", "build", "-t", IMAGE, str(EVALS)], check=True)

    def check(task):
        return [
            grade_ref(task, task["commit"]),
            grade_ref(task, f"{task['commit']}^"),
        ]

    invalid = 0
    with concurrent.futures.ThreadPoolExecutor(args.jobs) as pool:
        for task, (commit, parent) in zip(tasks, pool.map(check, tasks)):
            if commit is None and parent is not None:
                print(f"{task['id']}: valid; the parent fails: {parent}", flush=True)
                continue
            invalid += 1
            print(
                f"{task['id']}: INVALID; the commit"
                f" {'passes' if commit is None else 'fails: ' + commit}; the parent"
                f" {'passes' if parent is None else 'fails: ' + parent}",
                flush=True,
            )
    return 1 if invalid else 0


def grade_ref(task, ref):
    """Grades the task's ref in a fresh container."""
    container = docker("run", "-d", "--label", "ox-eval=validate", IMAGE, "sleep", "infinity")
    try:
        docker("exec", container, "mkdir", "/workspace")
        extract(container, task, ref)
        return grade(container, task)
    finally:
        docker("rm", "-f", container)


def docker(*args):
    return subprocess.run(
        ["docker", *args], check=True, capture_output=True, text=True
    ).stdout.strip()


def remove_containers(label):
    containers = docker("ps", "-aq", "--filter", f"label=ox-eval={label}").split()
    if containers:
        docker("rm", "-f", *containers)


def build(ref, label):
    """Builds ox-server and returns the binary, its commit, and whether the tree was dirty."""
    subprocess.run(["docker", "build", "-t", IMAGE, str(EVALS)], check=True)
    bin_dir = EVALS / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    if ref:
        commit = git("rev-parse", "--verify", f"{ref}^{{commit}}")
        dirty = False
        binary = bin_dir / f"ox-server-{commit}"
        if binary.exists():
            print(f"reusing {binary}", flush=True)
            return binary, commit, dirty
        worktree = EVALS / "worktree" / label
        git("worktree", "add", "--detach", str(worktree), commit)
        try:
            go_build(worktree, binary, label)
        finally:
            git("worktree", "remove", "--force", str(worktree))
    else:
        commit = git("rev-parse", "HEAD")
        dirty = bool(git("status", "--porcelain", "--untracked-files=no"))
        binary = bin_dir / f"ox-server-{label}"
        go_build(ROOT, binary, label)
    return binary, commit, dirty


def go_build(source, binary, label):
    """Cross-compiles ox-server for the Linux architecture of the containers."""
    goarch = {"x86_64": "amd64", "aarch64": "arm64"}[
        docker("info", "--format", "{{.Architecture}}")
    ]
    # A concurrent run that finds the binary never reads a partial file.
    partial = binary.with_name(f"{binary.name}.tmp-{label}")
    subprocess.run(
        ["go", "build", "-trimpath", "-o", str(partial), "./cmd/ox-server"],
        cwd=source,
        env=os.environ | {"GOOS": "linux", "GOARCH": goarch, "CGO_ENABLED": "0"},
        check=True,
    )
    os.replace(partial, binary)


def run_repetition(binary, api_key, args, identity, task, rep):
    run_dir = EVALS / "runs" / args.label / task["id"] / str(rep)
    # A directory without a result is from an interrupted run.
    if run_dir.exists():
        shutil.rmtree(run_dir)
    run_dir.mkdir(parents=True)
    container = f"ox-eval-{args.label}-{task['id']}-{rep}"
    docker(
        "run",
        "-d",
        "--name",
        container,
        "--label",
        f"ox-eval={args.label}",
        IMAGE,
        "sleep",
        "infinity",
    )
    try:
        docker("cp", str(binary), f"{container}:/usr/local/bin/ox-server")
        docker("exec", container, "mkdir", "/workspace", "/data")
        # A host behind a proxy that re-signs TLS names the proxy's
        # certificates in SSL_CERT_FILE; Ox in the container must trust them too.
        if os.environ.get("SSL_CERT_FILE"):
            subprocess.run(
                [
                    "docker",
                    "exec",
                    "-i",
                    container,
                    "sh",
                    "-c",
                    "cat >> /etc/ssl/certs/ca-certificates.crt",
                ],
                input=Path(os.environ["SSL_CERT_FILE"]).read_bytes(),
                check=True,
                capture_output=True,
            )
        if identity["providers"]:
            settings = {"models": {args.model: {"providers": identity["providers"]}}}
            subprocess.run(
                [
                    "docker",
                    "exec",
                    "-i",
                    container,
                    "sh",
                    "-c",
                    "mkdir -p ~/.config/ox && cat > ~/.config/ox/settings.json",
                ],
                input=json.dumps(settings),
                check=True,
                capture_output=True,
                text=True,
            )
        extract(container, task, f"{task['commit']}^")
        # The workspace is a fresh repository so the agent can use Git without
        # seeing later commits, and its changes are a diff from `base`.
        docker(
            "exec",
            "-w",
            "/workspace",
            container,
            "sh",
            "-c",
            "git config --global user.name Ox"
            " && git config --global user.email ox@example.com"
            " && git init -q && git add -A"
            " && git commit -qm 'Import the repository' && git tag base",
        )
        # Building the first parent before the agent's clock starts keeps the
        # build of its unchanged code out of the agent's time. The first task's
        # parent has nothing to build.
        subprocess.run(
            ["docker", "exec", "-w", "/workspace", container, "make"],
            capture_output=True,
        )

        started_at = datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds")
        start = time.monotonic()
        with (
            open(run_dir / "answer.txt", "w") as answer,
            open(run_dir / "stderr.txt", "w") as stderr,
        ):
            # The key comes from the docker process's environment, so it never
            # appears on a command line.
            returncode = subprocess.run(
                [
                    "docker",
                    "exec",
                    "-e",
                    "OPENROUTER_API_KEY",
                    "-e",
                    "OX_DATA_DIR=/data",
                    "-w",
                    "/workspace",
                    container,
                    "timeout",
                    "--signal=TERM",
                    f"--kill-after={KILL_GRACE}",
                    str(task.get("timeout", args.timeout)),
                    "ox-server",
                    "run",
                    "--dir",
                    "/workspace",
                    "--model",
                    args.model,
                    "--effort",
                    args.effort,
                    task["prompt"].strip(),
                ],
                stdin=subprocess.DEVNULL,
                stdout=answer,
                stderr=stderr,
                env=os.environ | {"OPENROUTER_API_KEY": api_key},
            ).returncode
        seconds = time.monotonic() - start
        # `timeout` exits 124 after SIGTERM and 137 after SIGKILL.
        if returncode in (124, 137):
            status = "timeout"
        elif returncode == 0:
            status = "finished"
        else:
            status = "failed"

        # The workspace holds build output, so only the agent's changes are kept.
        (run_dir / "changes.diff").write_bytes(
            subprocess.run(
                [
                    "docker",
                    "exec",
                    "-w",
                    "/workspace",
                    container,
                    "sh",
                    "-c",
                    "git add -A && git diff --cached --binary base",
                ],
                check=True,
                capture_output=True,
            ).stdout
        )
        failure = grade(container, task)
        docker("cp", f"{container}:/data", str(run_dir / "data"))
    finally:
        docker("rm", "-f", container)

    result = identity | {
        "task": task["id"],
        "rep": rep,
        "started_at": started_at,
        "status": status,
        "seconds": round(seconds, 1),
        "passed": failure is None,
        "failure": failure,
    }
    result |= transcript_metrics(run_dir / "data" / "ox.db")
    (run_dir / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


def transcript_metrics(database):
    """Sums the metrics over every session in a run's private database."""
    metrics = {
        "requests": 0,
        "tool_calls": 0,
        "failed_calls": 0,
        "cancelled_calls": 0,
        "calls_by_tool": {},
        "repeated_calls": 0,
        "input_tokens": 0,
        "cached_tokens": 0,
        "output_tokens": 0,
        "reasoning_tokens": 0,
        "cost": 0.0,
        "max_input_tokens": 0,
        "tool_output_chars": 0,
    }
    # Ox never opened its database when it failed before creating a session.
    if not database.exists():
        return metrics
    connection = sqlite3.connect(f"file:{database}?mode=ro", uri=True)
    try:
        rows = connection.execute(
            "SELECT session_id, kind, data FROM transcript_entries ORDER BY session_id, id"
        ).fetchall()
    finally:
        connection.close()

    seen_calls = set()
    for session_id, kind, data in rows:
        if kind not in ("assistant_batch", "compaction"):
            continue
        entry = json.loads(data)
        # A compaction is a model request too: the one that wrote its summary.
        usage = entry["usage"] if kind == "compaction" else entry["message"]["usage"]
        metrics["requests"] += 1
        if usage:
            metrics["input_tokens"] += usage["input_tokens"]
            metrics["cached_tokens"] += usage["cached_tokens"]
            metrics["output_tokens"] += usage["output_tokens"]
            metrics["reasoning_tokens"] += usage["reasoning_tokens"]
            # One usage without a cost makes the run's cost unavailable.
            if metrics["cost"] is not None:
                metrics["cost"] = (
                    None if usage["cost"] is None else metrics["cost"] + usage["cost"]
                )
            metrics["max_input_tokens"] = max(
                metrics["max_input_tokens"], usage["input_tokens"]
            )
        if kind == "compaction":
            continue
        for call, outcome in zip(entry["message"]["tool_calls"], entry["outcomes"]):
            metrics["tool_calls"] += 1
            by_tool = metrics["calls_by_tool"].setdefault(
                call["name"], {"calls": 0, "failed": 0}
            )
            by_tool["calls"] += 1
            if outcome["status"] == "failed":
                metrics["failed_calls"] += 1
                by_tool["failed"] += 1
            elif outcome["status"] == "cancelled":
                metrics["cancelled_calls"] += 1
            key = (session_id, call["name"], call["arguments"])
            if key in seen_calls:
                metrics["repeated_calls"] += 1
            seen_calls.add(key)
            metrics["tool_output_chars"] += len(outcome["text"])
    if metrics["cost"] is not None:
        metrics["cost"] = round(metrics["cost"], 6)
    return metrics


def compare(parser, labels):
    """Writes a Markdown comparison of the labels to evals/reports/ and prints its path."""
    results = {}
    for label in labels:
        paths = sorted((EVALS / "runs" / label).glob("*/*/result.json"))
        if not paths:
            parser.error(f"no results for {label}")
        results[label] = [json.loads(path.read_text()) for path in paths]
    # Totals and the chart compare only tasks that every label ran.
    task_sets = [{result["task"] for result in runs} for runs in results.values()]
    common = set.intersection(*task_sets)
    if not common:
        parser.error("the labels have no task in common")
    left_out = sorted(set.union(*task_sets) - common)
    results = {
        label: [result for result in runs if result["task"] in common]
        for label, runs in results.items()
    }

    left_out_note = (
        " Tasks without results for every label are left out: "
        + ", ".join(f"`{task}`" for task in left_out)
        + "."
        if left_out
        else ""
    )

    runs_rows = []
    for label, label_results in results.items():
        first = label_results[0]
        runs_rows.append(
            [
                label,
                f"`{first['commit'][:10]}`",
                "yes" if first["dirty"] else "no",
                f"`{first['model']}`",
                first["effort"],
                ", ".join(first["providers"]) or "any",
                str(len(label_results)),
            ]
        )

    task_ids = sorted(common)
    totals = {label: {} for label in labels}
    overview_rows = []
    task_sections = []
    for task_id in task_ids:
        by_label = {
            label: [result for result in runs if result["task"] == task_id]
            for label, runs in results.items()
        }
        tools = sorted(
            {
                name
                for runs in by_label.values()
                for result in runs
                for name in result["calls_by_tool"]
            }
        )
        rows = []
        for metric in METRICS:
            if metric == "status":
                rows.append(
                    text_row(
                        "status", [status_counts(runs) for runs in by_label.values()]
                    )
                )
                for label, runs in by_label.items():
                    finished, count = totals[label].get("finished", (0, 0))
                    totals[label]["finished"] = (
                        finished + sum(r["status"] == "finished" for r in runs),
                        count + len(runs),
                    )
            elif metric == "passed":
                rows.append(
                    text_row("passed", [passed_count(runs) for runs in by_label.values()])
                )
                for label, runs in by_label.items():
                    passed, count = totals[label].get("passed", (0, 0))
                    totals[label]["passed"] = (
                        passed + sum(r["passed"] for r in runs),
                        count + len(runs),
                    )
            elif metric == "calls_by_tool":
                for name in tools:
                    for field in ("calls", "failed"):
                        values = {
                            label: [
                                result["calls_by_tool"].get(name, {}).get(field, 0)
                                for result in runs
                            ]
                            for label, runs in by_label.items()
                        }
                        rows.append(
                            numeric_row(f"`{name}` {field}", "", values, totals)
                        )
            elif any(metric in result for runs in by_label.values() for result in runs):
                values = {
                    label: [result[metric] for result in runs]
                    for label, runs in by_label.items()
                }
                row = numeric_row(metric, metric, values, totals)
                rows.append(row)
                if metric == "cost":
                    overview_rows.append(
                        [
                            f"`{task_id}`",
                            *(passed_count(runs) for runs in by_label.values()),
                            *row[1:],
                        ]
                    )
        task_sections += [
            "",
            f"### `{task_id}`",
            "",
            *markdown_table(value_header("Metric", labels), rows),
        ]

    total_rows = [
        text_row(
            name,
            [f"{totals[label][name][0]}/{totals[label][name][1]}" for label in labels],
        )
        for name in ("finished", "passed")
    ]
    for metric in METRICS:
        if metric in ("status", "passed", "calls_by_tool") or all(
            metric not in totals[label] for label in labels
        ):
            continue
        sums = [totals[label].get(metric, 0) for label in labels]
        total_rows.append(
            [metric, *with_changes(sums, [number(metric, value) for value in sums])]
        )

    lines = [
        f"# Evaluation comparison: {', '.join(labels)}",
        "",
        "## Runs",
        "",
        *markdown_table(
            ["Label", "Commit", "Dirty", "Model", "Effort", "Providers", "Results"],
            runs_rows,
            numeric=False,
        ),
        "",
        # A chart compares a later label with the first, so one label has none.
        *(
            [
                "## Chart",
                "",
                *(
                    f"![{label} compared with {labels[0]}]({labels[0]}-vs-{label}.svg)"
                    for label in labels[1:]
                ),
                "",
            ]
            if len(labels) > 1
            else []
        ),
        "## Summary",
        "",
        *textwrap.wrap(
            f"Changes are relative to `{labels[0]}`. A task's values are medians"
            " over its repetitions, with the range in parentheses. Totals are sums"
            " of task medians." + left_out_note,
            80,
            break_long_words=False,
            break_on_hyphens=False,
        ),
        "",
        *markdown_table(value_header("Metric", labels), total_rows),
        "",
        "### Passed and cost by task",
        "",
        *markdown_table(
            [
                "Task",
                *(f"{label} passed" for label in labels),
                *value_header("", labels, " cost")[1:],
            ],
            overview_rows,
        ),
        "",
        "## Tasks",
        *task_sections,
    ]
    report = REPORTS / f"{'-vs-'.join(labels)}.md"
    REPORTS.mkdir(parents=True, exist_ok=True)
    report.write_text("\n".join(lines) + "\n")
    for label in labels[1:]:
        svg = REPORTS / f"{labels[0]}-vs-{label}.svg"
        svg.write_text(chart_svg(results, labels[0], label))
        subprocess.run(
            ["rsvg-convert", "--zoom", "2", "-o", str(svg.with_suffix(".png")), str(svg)],
            check=True,
        )
    print(report)


def chart_svg(results, base, candidate):
    """An SVG of how the candidate label changes each chart metric from the base
    label: across all tasks with a bootstrap interval, then task by task."""
    pad, name_width, column_width, row_height = 24, 170, 110, 30
    width = pad * 2 + name_width + len(CHART_METRICS) * column_width + column_width
    rng = random.Random(0)
    task_ids = sorted({r["task"] for label in (base, candidate) for r in results[label]})

    def values(label, task_id, key):
        return [r[key] for r in results[label] if r["task"] == task_id]

    def verdict(low, high):
        return "good" if high < 1 else "bad" if low > 1 else "neutral"

    def passed(values):
        return sum(values), len(values)

    def pass_verdict(base_passed, base_count, candidate_passed, count):
        more = candidate_passed * base_count - base_passed * count
        return "good" if more > 0 else "bad" if more < 0 else "neutral"

    def change(ratio):
        text = f"{ratio - 1:+.0%}"
        return "0%" if text[1:] == "0%" else text

    # overall[metric] is (low, point, high); each ratio is the candidate's median
    # over the base's median, and the point is their geometric mean over tasks.
    overall = {}
    for metric, _ in CHART_METRICS:
        pairs = [
            (values(base, task_id, metric), values(candidate, task_id, metric))
            for task_id in task_ids
        ]
        pairs = [
            (a, b)
            for a, b in pairs
            if a and b and None not in a + b and min(a) > 0 and min(b) > 0
        ]
        if not pairs:
            overall[metric] = None
            continue

        def mean_ratio(sample):
            return statistics.geometric_mean(
                statistics.median(sample(b)) / statistics.median(sample(a))
                for a, b in pairs
            )

        samples = sorted(
            mean_ratio(lambda v: rng.choices(v, k=len(v)))
            for _ in range(BOOTSTRAP_SAMPLES)
        )
        overall[metric] = (
            samples[int(0.05 * len(samples))],
            mean_ratio(lambda v: v),
            samples[int(0.95 * len(samples)) - 1],
        )

    def wrap(text):
        # 13px system-ui text averages about 6.4 pixels a character.
        return textwrap.wrap(text, int((width - 2 * pad) / 6.4))

    out = []
    y = pad + 16
    out.append(
        f'<text class="title" x="{pad}" y="{y}">{candidate} compared with {base}</text>'
    )
    y += 22
    base_passed, base_count = passed([r["passed"] for r in results[base]])
    candidate_passed, count = passed([r["passed"] for r in results[candidate]])
    pass_class = pass_verdict(base_passed, base_count, candidate_passed, count)
    out.append(
        f'<text class="secondary" x="{pad}" y="{y}">{results[base][0]["model"]}.'
        f" Passed: {base} {base_passed}/{base_count},"
        f' <tspan class="{pass_class}-text strong">{candidate}'
        f" {candidate_passed}/{count}</tspan>.</text>"
    )

    y += 44
    out.append(f'<text class="heading" x="{pad}" y="{y}">Across all tasks</text>')
    for line in wrap(
        "Each dot is the geometric mean over tasks of the candidate's median over the"
        " base's median, with a 90% bootstrap interval. Green is clearly lower, red is"
        " clearly higher, and gray is within the noise."
    ):
        y += 18
        out.append(f'<text class="secondary" x="{pad}" y="{y}">{line}</text>')
    left = pad + name_width
    rows = {}
    for metric, _ in CHART_METRICS:
        if overall[metric]:
            low, point, high = overall[metric]
            kind = verdict(low, high)
            word = {"good": "Lower", "bad": "Higher", "neutral": "No clear change"}[kind]
            rows[metric] = kind, f"{change(point)} ({change(low)} to {change(high)})", word
    # The chart rows fill the width: the metric names, the plot, each change
    # right-aligned, then each verdict. Bold 13px text averages about 7 pixels
    # a character, and the changes' tabular digits about 7.5.
    verdict_x = width - pad - 7 * max((len(row[2]) for row in rows.values()), default=0)
    summary_x = verdict_x - 16
    plot_left = pad + 7 * max(len(title) for _, title in CHART_METRICS) + 24
    plot_right = (
        summary_x
        - 7.5 * max((len(row[1]) for row in rows.values()), default=len("unavailable"))
        - 24
    )
    # The axis spans no change and every interval, so the plot is not left half
    # empty when every change is one way, and at least ±10% so noise is not
    # magnified. The inset leaves room for the dots and tick labels.
    logs = [math.log(v) for r in overall.values() if r for v in r]
    low_end, high_end = min([0, *logs]), max([0, *logs])
    widen = max(0, 2 * math.log(1.1) - (high_end - low_end)) / 2
    low_end, high_end = low_end - widen, high_end + widen
    inset = 16

    def x(ratio):
        return plot_left + inset + (math.log(ratio) - low_end) / (
            high_end - low_end
        ) * (plot_right - plot_left - 2 * inset)

    # Ticks step out from no change, each at least 56 pixels from the last.
    ticks = [1]
    middle = CHART_TICKS.index(1)
    for side in (reversed(CHART_TICKS[:middle]), CHART_TICKS[middle + 1 :]):
        last = 1
        for tick in side:
            if low_end <= math.log(tick) <= high_end and abs(x(tick) - x(last)) >= 56:
                ticks.append(tick)
                last = tick
    rows_top = y + 16
    axis_y = rows_top + len(CHART_METRICS) * row_height + 8
    for tick in ticks:
        out.append(
            f'<line class="{"base" if tick == 1 else "grid"}" x1="{x(tick):.1f}"'
            f' y1="{rows_top}" x2="{x(tick):.1f}" y2="{axis_y - 8}"/>'
            f'<text class="muted" x="{x(tick):.1f}" y="{axis_y + 6}"'
            f' text-anchor="middle">{change(tick)}</text>'
        )
    out.append(
        f'<text class="muted" x="{x(1) - 8:.1f}" y="{axis_y + 24}"'
        ' text-anchor="end">← lower</text>'
        f'<text class="muted" x="{x(1) + 8:.1f}" y="{axis_y + 24}">higher →</text>'
    )
    for row, (metric, title) in enumerate(CHART_METRICS):
        cy = rows_top + row * row_height + row_height / 2
        if metric not in rows:
            out.append(
                f'<text class="strong" x="{pad}" y="{cy + 4}">{title}</text>'
                f'<text class="muted" x="{summary_x}" y="{cy + 4}" text-anchor="end">'
                "unavailable</text>"
            )
            continue
        low, point, high = overall[metric]
        kind, summary, word = rows[metric]
        out.append(
            f"<g><title>{title}: {summary}</title>"
            f'<rect x="{pad}" y="{cy - row_height / 2}" width="{width - 2 * pad}"'
            f' height="{row_height}" fill="transparent"/>'
            f'<text class="strong" x="{pad}" y="{cy + 4}">{title}</text>'
            f'<line class="{kind}" x1="{x(low):.1f}" y1="{cy}" x2="{x(high):.1f}"'
            f' y2="{cy}" stroke-width="2" stroke-linecap="round"/>'
            f'<circle class="{kind}" cx="{x(point):.1f}" cy="{cy}" r="6"/>'
            f'<text x="{summary_x}" y="{cy + 4}" text-anchor="end">{summary}</text>'
            f'<text class="{kind}-text strong" x="{verdict_x}" y="{cy + 4}">'
            f"{word}</text></g>"
        )

    y = axis_y + 64
    out.append(f'<text class="heading" x="{pad}" y="{y}">By task</text>')
    for line in wrap(
        "Each cell is the change in the task's median. A cell is colored only when"
        " every repetition of the candidate was lower (green) or higher (red) than"
        " every repetition of the base; hover a cell for the values."
    ):
        y += 18
        out.append(f'<text class="secondary" x="{pad}" y="{y}">{line}</text>')
    y += 34
    columns = [("passed", "Passed"), *CHART_METRICS]
    for column, (_, title) in enumerate(columns):
        out.append(
            f'<text class="strong" x="{left + (column + 1) * column_width - 12}"'
            f' y="{y}" text-anchor="end">{title}</text>'
        )
    y += 10
    for task_id in task_ids:
        out.append(
            f'<line class="grid" x1="{pad}" y1="{y}" x2="{width - pad}" y2="{y}"/>'
            f'<text x="{pad}" y="{y + 19}">{task_id}</text>'
        )
        for column, (metric, title) in enumerate(columns):
            a, b = values(base, task_id, metric), values(candidate, task_id, metric)
            if metric == "passed":
                (fa, na), (fb, nb) = passed(a), passed(b)
                text = f"{fb}/{nb}" if fa * nb == fb * na else f"{fa}/{na} → {fb}/{nb}"
                kind = pass_verdict(fa, na, fb, nb)
                tip = f"{base} {fa}/{na}; {candidate} {fb}/{nb}"
            else:
                if not a or not b or None in a + b or not statistics.median(a):
                    text, kind = "-", "neutral"
                else:
                    text = change(statistics.median(b) / statistics.median(a))
                    kind = verdict(min(b) / max(a), max(b) / min(a))
                tip = "; ".join(
                    f"{label} "
                    + ", ".join(number(metric, v) for v in sorted(vs, key=lambda v: v or 0))
                    for label, vs in ((base, a), (candidate, b))
                )
            cell_left = left + column * column_width
            fill = (
                f'<rect class="{kind} tint" x="{cell_left + 4}" y="{y + 3}"'
                f' width="{column_width - 8}" height="{row_height - 6}" rx="4"/>'
                if kind != "neutral"
                else ""
            )
            out.append(
                f"<g><title>{task_id}, {title.lower()}: {tip}</title>"
                f'<rect x="{cell_left}" y="{y}" width="{column_width}"'
                f' height="{row_height}" fill="transparent"/>{fill}'
                f'<text class="{"strong" if fill else "muted"}"'
                f' x="{cell_left + column_width - 12}" y="{y + 19}"'
                f' text-anchor="end">{text}</text></g>'
            )
        y += row_height
    height = y + pad

    return "\n".join(
        [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}"'
            f' height="{height}" viewBox="0 0 {width} {height}">',
            "<style>",
            "text { font-family: system-ui, -apple-system, 'Helvetica Neue', Arial,"
            " sans-serif; font-size: 13px; fill: #0b0b0b;"
            " font-variant-numeric: tabular-nums; }",
            ".secondary { fill: #52514e; } .muted { fill: #898781; }",
            ".title { font-size: 18px; font-weight: 600; }",
            ".heading { font-size: 15px; font-weight: 600; } .strong { font-weight: 600; }",
            ".surface { fill: #fcfcfb; } .grid { stroke: #e1e0d9; } .base { stroke: #c3c2b7; }",
            ".good { fill: #0ca30c; } line.good { stroke: #0ca30c; }",
            ".bad { fill: #d03b3b; } line.bad { stroke: #d03b3b; }",
            ".neutral { fill: #898781; } line.neutral { stroke: #898781; }",
            ".good-text { fill: #006300; } .bad-text { fill: #b02a2a; }"
            " .neutral-text { fill: #52514e; }",
            ".tint { stroke: none; fill-opacity: 0.16; }",
            "circle { stroke: #fcfcfb; stroke-width: 2px; }",
            "@media (prefers-color-scheme: dark) {",
            "text { fill: #ffffff; } .secondary, .neutral-text { fill: #c3c2b7; }",
            ".good-text { fill: #0ca30c; } .bad-text { fill: #e06464; }",
            ".surface { fill: #1a1a19; } .grid { stroke: #2c2c2a; } .base { stroke: #383835; }",
            ".tint { fill-opacity: 0.28; } circle { stroke: #1a1a19; }",
            "}",
            "</style>",
            f'<rect class="surface" width="{width}" height="{height}"/>',
            *out,
            "</svg>",
        ]
    ) + "\n"


def status_counts(runs):
    counts = {}
    for result in runs:
        counts[result["status"]] = counts.get(result["status"], 0) + 1
    return ", ".join(f"{status} {count}" for status, count in sorted(counts.items()))


def passed_count(runs):
    return f"{sum(result['passed'] for result in runs)}/{len(runs)}"


def value_header(first, labels, suffix=""):
    """A first column, then each label's values, with a change column after each later label."""
    header = [first, f"{labels[0]}{suffix}"]
    for label in labels[1:]:
        header += [
            f"{label}{suffix}",
            "Change" if len(labels) == 2 else f"{label} change",
        ]
    return header


def text_row(name, cells):
    """A row of values that have no percentage change."""
    return [name, *with_changes([None] * len(cells), cells)]


def numeric_row(name, metric, values, totals):
    """One row of medians with ranges, adding each median to the label's total."""
    medians = []
    cells = []
    for label, label_values in values.items():
        if not label_values or None in label_values:
            medians.append(None)
            cells.append("unavailable" if label_values else "-")
            if label_values and metric:
                totals[label][metric] = None
            continue
        median = statistics.median(label_values)
        medians.append(median)
        if metric and totals[label].get(metric, 0) is not None:
            totals[label][metric] = totals[label].get(metric, 0) + median
        low, high = min(label_values), max(label_values)
        cell = number(metric, median)
        if low != high:
            cell += f" ({number(metric, low)}–{number(metric, high)})"
        cells.append(cell)
    return [name, *with_changes(medians, cells)]


def with_changes(values, cells):
    """Follows each later cell with its percentage change from the first value."""
    base = values[0]
    changed = [cells[0]]
    for value, cell in zip(values[1:], cells[1:]):
        change = (
            f"{(value - base) / base * 100:+.0f}%" if base and value is not None else ""
        )
        changed += [cell, change]
    return changed


def number(metric, value):
    if value is None:
        return "unavailable"
    if metric == "cost":
        return f"${value:.4f}"
    if metric == "seconds" or float(value).is_integer():
        return f"{value:.0f}"
    return f"{value:.1f}"


def markdown_table(header, rows, numeric=True):
    """A padded Markdown table whose columns after the first are right-aligned when numeric."""
    widths = [
        max(3, *(len(row[column]) for row in [header, *rows]))
        for column in range(len(header))
    ]

    def line(cells):
        return (
            "| "
            + " | ".join(
                cell.rjust(width) if numeric and column else cell.ljust(width)
                for column, (cell, width) in enumerate(zip(cells, widths))
            )
            + " |"
        )

    divider = [
        "-" * (width - 1) + ":" if numeric and column else "-" * width
        for column, width in enumerate(widths)
    ]
    return [line(header), "| " + " | ".join(divider) + " |", *map(line, rows)]


raise SystemExit(main())
