#!/usr/bin/env python3
# Runs the small-c benchmark tasks in scripts/bench/tasks.toml against one build
# of ox, each repetition in its own Docker container, validates the tasks, and
# compares labeled benchmark runs.
#
#   scripts/bench.py run --label base --ref main --model MODEL_ID --effort LEVEL
#   scripts/bench.py run --label change --model MODEL_ID --effort LEVEL --tests hidden
#   scripts/bench.py validate [--task ID ...]
#   scripts/bench.py compare base change    # writes agents/evals/base-vs-change.md
#
# `run --tests visible|examples|hidden` starts the workspace with all, the
# task's examples, or none of the task's new test cases. Running an existing
# label again runs only its repetitions without a result. Models use
# OPENROUTER_API_KEY from .env.

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
import textwrap
import time
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BENCH = ROOT / "bench"
EVALS = ROOT / "agents" / "evals"
TASKS = ROOT / "scripts" / "bench" / "tasks.toml"
IMAGE = "ox-bench"
# Seconds between SIGTERM and SIGKILL for a timed-out run.
KILL_GRACE = 30
# Attempts per repetition. An attempt that times out or where Ox fails is
# retried; failed tests are a result.
ATTEMPTS = 4
# Seconds allowed for scoring with `go test`.
SCORE_TIMEOUT = 600
# The sentence appended to the prompt for each `run --tests` setting.
TEST_NOTES = {
    "visible": "The tests/ directory includes every new test case that will score"
    " this task.",
    "examples": "The tests/ directory includes some of the new test cases that will"
    " score this task; the others are held back.",
    "hidden": "The new test cases that will score this task are held back; none of"
    " them are in the tests/ directory.",
}

METRICS = [
    "passed",
    "tests_passed",
    "tests_total",
    "regressions",
    "status",
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
# The chart's metrics, where lower is better, and its axis tick sets from finest
# to coarsest.
CHART_METRICS = [
    ("cost", "Cost"),
    ("seconds", "Time"),
    ("input_tokens", "Input tokens"),
    ("output_tokens", "Output tokens"),
    ("requests", "Requests"),
]
CHART_TICKS = [
    [0.8, 0.9, 1, 1.1, 1.25],
    [0.67, 0.8, 1, 1.25, 1.5],
    [0.5, 0.67, 1, 1.5, 2],
    [0.5, 1, 2, 3],
    [0.25, 0.5, 1, 2, 4],
    [0.1, 0.2, 0.5, 1, 2, 5, 10],
]
BOOTSTRAP_SAMPLES = 2000


def main():
    parser = argparse.ArgumentParser()
    commands = parser.add_subparsers(dest="command", required=True)
    run_parser = commands.add_parser("run")
    run_parser.add_argument("--label", required=True)
    run_parser.add_argument("--model", required=True, metavar="MODEL_ID")
    run_parser.add_argument("--effort", required=True, metavar="LEVEL")
    run_parser.add_argument("--ref", metavar="GIT_REF")
    run_parser.add_argument("--tests", choices=list(TEST_NOTES), default="examples")
    run_parser.add_argument("--reps", type=int, default=3)
    run_parser.add_argument("--jobs", type=int, default=4)
    run_parser.add_argument("--timeout", type=int, default=1800, metavar="SECONDS")
    run_parser.add_argument("--task", nargs="+", dest="tasks", metavar="ID")
    validate_parser = commands.add_parser("validate")
    validate_parser.add_argument("--task", nargs="+", dest="tasks", metavar="ID")
    compare_parser = commands.add_parser("compare")
    compare_parser.add_argument("labels", nargs="+", metavar="LABEL")
    args = parser.parse_args()
    if args.command == "run":
        run(parser, args)
    elif args.command == "validate":
        validate(parser, args)
    else:
        compare(parser, args.labels)


def run(parser, args):
    if not re.fullmatch(r"[A-Za-z0-9._-]+", args.label):
        parser.error("--label may contain only letters, digits, '.', '_', and '-'")
    label_dir = BENCH / "runs" / args.label
    repo, all_tasks = load_tasks()
    tasks = select_tasks(parser, all_tasks, args.tasks)
    for task in tasks:
        try:
            resolve_task(repo, task)
        except TaskError as error:
            parser.error(f"{task['id']}: {error}")

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
        "tests": args.tests,
        "providers": pinned_providers(args.model),
    }
    solutions = {task["id"]: task["solution"] for task in tasks}
    for saved_path in label_dir.glob("*/*/result.json"):
        saved = json.loads(saved_path.read_text())
        if any(
            saved.get(key) != identity[key]
            for key in ("commit", "dirty", "model", "effort", "tests", "providers")
        ):
            parser.error(
                f"{label_dir} holds results for a different commit, model, effort,"
                " tests setting, or providers"
            )
        if (
            saved["task"] in solutions
            and saved.get("solution") != solutions[saved["task"]]
        ):
            parser.error(
                f"{label_dir} holds results for a different commit of task/{saved['task']}"
            )
    runs = []
    for rep in range(1, args.reps + 1):
        for task in tasks:
            if not (label_dir / task["id"] / str(rep) / "result.json").exists():
                runs.append((task, rep))
    with concurrent.futures.ThreadPoolExecutor(args.jobs) as pool:
        futures = [
            pool.submit(
                run_repetition, binary, api_key, args, identity, repo, task, rep
            )
            for task, rep in runs
        ]
        try:
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                print(
                    f"{result['task']} {result['rep']}: {result['status']},"
                    f" {'passed' if result['passed'] else 'failed'},"
                    f" {result['tests_passed']}/{result['tests_total']} tests,"
                    f" {result['regressions']} regressions,"
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


def validate(parser, args):
    """Checks each task's tag, examples, and tests, and prints one line per task."""
    repo, all_tasks = load_tasks()
    tasks = select_tasks(parser, all_tasks, args.tasks)
    build_image()
    invalid = False
    for task in tasks:
        try:
            validate_task(repo, task)
        except TaskError as error:
            invalid = True
            print(f"{task['id']}: invalid: {error}", flush=True)
        else:
            print(f"{task['id']}: valid", flush=True)
    if invalid:
        raise SystemExit(1)


def validate_task(repo, task):
    """Raises TaskError unless the task's new tests fail at its base, its old
    tests pass there, and every test passes at its commit."""
    resolve_task(repo, task)
    out_dir = BENCH / "validate" / task["id"]
    if out_dir.exists():
        shutil.rmtree(out_dir)
    out_dir.mkdir(parents=True)
    all_tests = task["new_tests"] | task["old_tests"]
    for tree in ("base", "solution"):
        container = f"ox-bench-validate-{task['id']}-{tree}"
        start_container(container, "validate")
        try:
            copy_tree(container, repo, task[tree])
            check = out_dir / f"{tree}.txt"
            fields, outcomes = score(container, repo, task, check)
        finally:
            docker("rm", "-f", container)
        missing = sorted(all_tests - outcomes.keys())
        if missing:
            raise TaskError(
                f"at the {tree}, no result for {', '.join(missing)}; see {check}"
            )
        if tree == "base":
            failed_old = sorted(t for t in task["old_tests"] if outcomes[t] != "pass")
            if failed_old:
                raise TaskError(
                    f"old tests fail at the base: {', '.join(failed_old)}; see {check}"
                )
            if fields["tests_passed"] == fields["tests_total"]:
                raise TaskError(
                    f"every new test already passes at the base; see {check}"
                )
        elif not fields["passed"]:
            raise TaskError(f"tests fail at task/{task['id']}; see {check}")


def pinned_providers(model):
    """The providers pinned for the model in the global settings file, or []."""
    path = Path.home() / ".config" / "ox" / "settings.json"
    if not path.exists():
        return []
    models = json.loads(path.read_text()).get("models", {})
    return models.get(model, {}).get("providers", [])


class TaskError(Exception):
    """A problem with a task's definition or its commits in small-c."""


def load_tasks():
    """Returns the small-c repository path and the task list."""
    data = tomllib.loads(TASKS.read_text())
    ids = [task["id"] for task in data["task"]]
    duplicates = sorted({task_id for task_id in ids if ids.count(task_id) > 1})
    if duplicates:
        raise SystemExit(f"{TASKS}: duplicate task ids: {', '.join(duplicates)}")
    return (ROOT / data["repo"]).resolve(), data["task"]


def select_tasks(parser, tasks, ids):
    if not ids:
        return tasks
    unknown = set(ids) - {task["id"] for task in tasks}
    if unknown:
        parser.error(f"unknown tasks: {', '.join(sorted(unknown))}")
    return [task for task in tasks if task["id"] in ids]


def resolve_task(repo, task):
    """Adds the task's commits, new test files, and test names to the task."""
    task["base"], task["solution"] = task_commits(repo, task)
    task["new_files"] = new_test_files(repo, task["base"], task["solution"])
    for example in task.get("examples", []):
        if not any(
            str(Path(path).relative_to("tests").with_suffix("")) == example
            for path in task["new_files"]
        ):
            raise TaskError(f"the example {example} matches no new test")
    task["new_tests"] = test_ids(task["new_files"])
    all_files = git("ls-tree", "-r", "--name-only", task["solution"], "tests", cwd=repo)
    task["old_tests"] = test_ids(all_files.splitlines()) - task["new_tests"]


def task_commits(repo, task):
    """The hashes of the task's base, the first parent of task/<id>, and of the
    task commit."""
    tag = f"task/{task['id']}"
    hashes = []
    for rev, problem in (
        (f"refs/tags/{tag}^{{commit}}", f"the tag {tag} does not exist in {repo}"),
        (f"refs/tags/{tag}^1", f"the commit tagged {tag} has no parent"),
    ):
        result = subprocess.run(
            ["git", "rev-parse", "--verify", "--quiet", rev],
            cwd=repo,
            capture_output=True,
            text=True,
        )
        if result.returncode:
            raise TaskError(problem)
        hashes.append(result.stdout.strip())
    solution, base = hashes
    return base, solution


def new_test_files(repo, base, solution):
    """The paths under tests/ that the task commit adds or modifies."""
    return git(
        "diff",
        "--name-only",
        "--no-renames",
        "--diff-filter=AM",
        base,
        solution,
        "--",
        "tests",
        cwd=repo,
    ).splitlines()


def test_ids(paths):
    """The Go subtest names of the test cases among the paths. Files directly in
    tests/ are the harness, not test cases."""
    ids = set()
    for path in paths:
        parts = Path(path).parts
        if len(parts) == 3 and parts[0] == "tests":
            ids.add(f"TestCompiler/{parts[1]}/{Path(parts[2]).stem}")
    return ids


def visible_files(task, tests):
    """The new test case files the workspace starts with under a --tests setting."""
    if tests == "hidden":
        return []
    files = [path for path in task["new_files"] if len(Path(path).parts) == 3]
    if tests == "visible":
        return files
    examples = set(task.get("examples", []))
    return [
        path
        for path in files
        if str(Path(path).relative_to("tests").with_suffix("")) in examples
    ]


def git(*args, cwd=ROOT):
    return subprocess.run(
        ["git", *args], cwd=cwd, check=True, capture_output=True, text=True
    ).stdout.strip()


def docker(*args):
    return subprocess.run(
        ["docker", *args], check=True, capture_output=True, text=True
    ).stdout.strip()


def remove_containers(label):
    containers = docker("ps", "-aq", "--filter", f"label=ox-bench={label}").split()
    if containers:
        docker("rm", "-f", *containers)


def build_image():
    subprocess.run(["docker", "build", "-t", IMAGE, str(TASKS.parent)], check=True)


def build(ref, label):
    """Builds ox-server and returns the binary, its commit, and whether the tree was dirty."""
    build_image()
    bin_dir = BENCH / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    if ref:
        commit = git("rev-parse", "--verify", f"{ref}^{{commit}}")
        dirty = False
        binary = bin_dir / f"ox-server-{commit}"
        if binary.exists():
            print(f"reusing {binary}", flush=True)
            return binary, commit, dirty
        worktree = BENCH / "worktree" / label
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
    # A concurrent benchmark run that finds the binary never reads a partial
    # file.
    partial = binary.with_name(f"{binary.name}.tmp-{label}")
    subprocess.run(
        ["go", "build", "-trimpath", "-o", str(partial), "./cmd/ox-server"],
        cwd=source,
        env=os.environ | {"GOOS": "linux", "GOARCH": goarch, "CGO_ENABLED": "0"},
        check=True,
    )
    os.replace(partial, binary)


def run_repetition(binary, api_key, args, identity, repo, task, rep):
    run_dir = BENCH / "runs" / args.label / task["id"] / str(rep)
    for attempt in range(1, ATTEMPTS + 1):
        # A directory without a result is from an interrupted or retried attempt.
        if run_dir.exists():
            shutil.rmtree(run_dir)
        result = attempt_repetition(
            binary, api_key, args, identity, repo, task, rep, run_dir
        )
        if result["status"] == "finished" or attempt == ATTEMPTS:
            break
        print(f"{task['id']} {rep}: {result['status']}, retrying", flush=True)
    result["attempts"] = attempt
    (run_dir / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


def start_container(name, label):
    """Starts an idle benchmark container with an empty /workspace."""
    docker(
        "run",
        "-d",
        "--name",
        name,
        "--label",
        f"ox-bench={label}",
        IMAGE,
        "sleep",
        "infinity",
    )
    docker("exec", name, "mkdir", "/workspace")


def copy_tree(container, repo, commit, paths=()):
    """Copies the paths, or the whole tree, at a small-c commit into /workspace."""
    archive = subprocess.run(
        ["git", "archive", "--format=tar", commit, "--", *paths],
        cwd=repo,
        check=True,
        capture_output=True,
    ).stdout
    subprocess.run(
        ["docker", "cp", "-", f"{container}:/workspace"],
        input=archive,
        check=True,
        capture_output=True,
    )


def prepare_workspace(container, repo, task, tests, run_dir):
    """Fills /workspace with the base tree, the task commit's test harness, and
    the visible new test case files, as a repository with one commit, so the
    task commit is out of the agent's reach."""
    copy_tree(container, repo, task["base"])
    docker(
        "exec",
        "-w",
        "/workspace",
        container,
        "find",
        "tests",
        "-mindepth",
        "1",
        "-maxdepth",
        "1",
        "-type",
        "f",
        "-delete",
    )
    harness = [
        line.split("\t", 1)[1]
        for line in git("ls-tree", task["solution"], "tests/", cwd=repo).splitlines()
        if line.split()[1] == "blob"
    ]
    copy_tree(container, repo, task["solution"], harness + visible_files(task, tests))
    docker("exec", "-w", "/workspace", container, "git", "init", "-q")
    docker("exec", "-w", "/workspace", container, "git", "add", "-A")
    docker(
        "exec",
        "-w",
        "/workspace",
        container,
        "git",
        "-c",
        "user.name=ox-bench",
        "-c",
        "user.email=ox-bench@localhost",
        "commit",
        "-q",
        "-m",
        "Start the task",
    )
    files = docker("exec", "-w", "/workspace", container, "git", "ls-files")
    (run_dir / "start.txt").write_text(files + "\n")


def score(container, repo, task, check_path):
    """Restores the task commit's tests/, runs them, and writes their output to
    check_path. Returns the result fields and each test's last action."""
    docker("exec", container, "rm", "-rf", "/workspace/tests")
    copy_tree(container, repo, task["solution"], ["tests"])
    process = subprocess.run(
        [
            "docker",
            "exec",
            "-w",
            "/workspace",
            container,
            "timeout",
            str(SCORE_TIMEOUT),
            "go",
            "test",
            "-json",
            "-count=1",
            "./tests",
        ],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
    )
    outcomes = {}
    output = []
    for line in process.stdout.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            output.append(line + "\n")
            continue
        output.append(event.get("Output", ""))
        if "Test" in event and event.get("Action") in ("pass", "fail", "skip"):
            outcomes[event["Test"]] = event["Action"]
    check_path.write_text("".join(output) + process.stderr)
    # A test case with no pass result, such as after a build failure, failed.
    tests_passed = sum(outcomes.get(test) == "pass" for test in task["new_tests"])
    regressions = sum(outcomes.get(test) != "pass" for test in task["old_tests"])
    fields = {
        "passed": tests_passed == len(task["new_tests"])
        and regressions == 0
        and process.returncode == 0,
        "tests_passed": tests_passed,
        "tests_total": len(task["new_tests"]),
        "regressions": regressions,
    }
    return fields, outcomes


def attempt_repetition(binary, api_key, args, identity, repo, task, rep, run_dir):
    run_dir.mkdir(parents=True)
    container = f"ox-bench-{args.label}-{task['id']}-{rep}"
    start_container(container, args.label)
    try:
        docker("cp", str(binary), f"{container}:/usr/local/bin/ox-server")
        docker("exec", container, "mkdir", "/data")
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
        prepare_workspace(container, repo, task, args.tests, run_dir)

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
                    str(args.timeout),
                    "ox-server",
                    "run",
                    "--dir",
                    "/workspace",
                    "--model",
                    args.model,
                    "--effort",
                    args.effort,
                    f"{task['prompt']}\n\n{TEST_NOTES[args.tests]}",
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

        docker("cp", f"{container}:/workspace", str(run_dir / "workspace"))
        fields, _ = score(container, repo, task, run_dir / "check.txt")
        docker("cp", f"{container}:/data", str(run_dir / "data"))
    finally:
        docker("rm", "-f", container)

    result = identity | {
        "task": task["id"],
        "rep": rep,
        "solution": task["solution"],
        "started_at": started_at,
        "status": status,
        "seconds": round(seconds, 1),
    }
    return result | fields | transcript_metrics(run_dir / "data" / "ox.db")


def transcript_metrics(database):
    """Sums the metrics over every session in a benchmark run's private database."""
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
        entry = json.loads(data)
        if kind == "assistant_batch":
            message = entry["message"]
            metrics["requests"] += 1
            usage = message["usage"]
            if usage:
                metrics["input_tokens"] += usage["input_tokens"]
                # Transcripts from commits before these counts were recorded lack them.
                metrics["cached_tokens"] += usage.get("cached_tokens", 0)
                metrics["output_tokens"] += usage["output_tokens"]
                metrics["reasoning_tokens"] += usage.get("reasoning_tokens", 0)
                # One usage without a cost makes the run's cost unavailable.
                if metrics["cost"] is not None:
                    metrics["cost"] = (
                        None if usage["cost"] is None else metrics["cost"] + usage["cost"]
                    )
                metrics["max_input_tokens"] = max(
                    metrics["max_input_tokens"], usage["input_tokens"]
                )
            for call, outcome in zip(message["tool_calls"], entry["outcomes"]):
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
    """Writes a Markdown comparison of the labels to agents/evals/ and prints its path."""
    results = {}
    for label in labels:
        paths = sorted((BENCH / "runs" / label).glob("*/*/result.json"))
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
                first["tests"],
                ", ".join(first.get("providers", [])) or "any",
                str(len(label_results)),
            ]
        )

    task_ids = sorted({result["task"] for runs in results.values() for result in runs})
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
            if metric == "passed":
                rows.append(
                    text_row("passed", [pass_rate(runs) for runs in by_label.values()])
                )
                for label, runs in by_label.items():
                    passed, count = totals[label].get("passed", (0, 0))
                    totals[label]["passed"] = (
                        passed + sum(result["passed"] for result in runs),
                        count + len(runs),
                    )
            elif metric == "status":
                rows.append(
                    text_row(
                        "status", [status_counts(runs) for runs in by_label.values()]
                    )
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
            else:
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
                            *(pass_rate(runs) for runs in by_label.values()),
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
            "passed",
            [
                f"{totals[label]['passed'][0]}/{totals[label]['passed'][1]}"
                for label in labels
            ],
        )
    ]
    for metric in METRICS:
        if metric in ("passed", "status", "calls_by_tool"):
            continue
        sums = [totals[label].get(metric, 0) for label in labels]
        total_rows.append(
            [metric, *with_changes(sums, [number(metric, value) for value in sums])]
        )

    lines = [
        f"# Benchmark comparison: {', '.join(labels)}",
        "",
        "## Runs",
        "",
        *markdown_table(
            [
                "Label",
                "Commit",
                "Dirty",
                "Model",
                "Effort",
                "Tests",
                "Providers",
                "Results",
            ],
            runs_rows,
            numeric=False,
        ),
        "",
        "## Chart",
        "",
        *(
            f"![{label} compared with {labels[0]}]({labels[0]}-vs-{label}.svg)"
            for label in labels[1:]
        ),
        "",
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
        "### Pass rate and cost by task",
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
    report = EVALS / f"{'-vs-'.join(labels)}.md"
    EVALS.mkdir(parents=True, exist_ok=True)
    report.write_text("\n".join(lines) + "\n")
    for label in labels[1:]:
        svg = EVALS / f"{labels[0]}-vs-{label}.svg"
        svg.write_text(chart_svg(results, labels[0], label))
        subprocess.run(
            ["rsvg-convert", "--zoom", "2", "-o", str(svg.with_suffix(".png")), str(svg)],
            check=True,
        )
    print(report)


def chart_svg(results, base, candidate):
    """An SVG of how the candidate label changes each chart metric from the base
    label: across all tasks with a bootstrap interval, then task by task."""
    pad, name_width, plot_width, column_width, row_height = 24, 170, 360, 110, 30
    width = pad * 2 + name_width + len(CHART_METRICS) * column_width + column_width
    rng = random.Random(0)
    task_ids = sorted({r["task"] for label in (base, candidate) for r in results[label]})

    def values(label, task_id, key):
        return [r[key] for r in results[label] if r["task"] == task_id]

    def verdict(low, high):
        return "good" if high < 1 else "bad" if low > 1 else "neutral"

    def passed(flags):
        return sum(flags), len(flags)

    def pass_verdict(base_passed, base_count, passed_count, count):
        more = passed_count * base_count - base_passed * count
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
        return textwrap.wrap(text, 125)

    out = []
    y = pad + 16
    out.append(
        f'<text class="title" x="{pad}" y="{y}">{candidate} compared with {base}</text>'
    )
    y += 22
    base_passed, base_count = passed([r["passed"] for r in results[base]])
    passed_count, count = passed([r["passed"] for r in results[candidate]])
    pass_class = pass_verdict(base_passed, base_count, passed_count, count)
    out.append(
        f'<text class="secondary" x="{pad}" y="{y}">{results[base][0]["model"]}.'
        f' Passed: {base} {base_passed}/{base_count},'
        f' <tspan class="{pass_class}-text strong">{candidate} {passed_count}/{count}</tspan>.'
        "</text>"
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
    reach = 1.1 * max(
        math.log(1.1),
        *(abs(math.log(x)) for r in overall.values() if r for x in r),
    )

    def x(ratio):
        return left + (math.log(ratio) + reach) / (2 * reach) * plot_width

    ticks = [1]
    for candidates in CHART_TICKS:
        visible = [t for t in candidates if abs(math.log(t)) <= reach]
        if len(visible) >= 3 and all(
            b - a >= 44 for a, b in zip(map(x, visible), map(x, visible[1:]))
        ):
            ticks = visible
            break
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
        f'<text class="muted" x="{left}" y="{axis_y + 24}">← lower</text>'
        f'<text class="muted" x="{left + plot_width}" y="{axis_y + 24}"'
        ' text-anchor="end">higher →</text>'
    )
    for row, (metric, title) in enumerate(CHART_METRICS):
        cy = rows_top + row * row_height + row_height / 2
        if overall[metric] is None:
            out.append(
                f'<text class="strong" x="{pad}" y="{cy + 4}">{title}</text>'
                f'<text class="muted" x="{left + plot_width + 24}" y="{cy + 4}">'
                "unavailable</text>"
            )
            continue
        low, point, high = overall[metric]
        kind = verdict(low, high)
        summary = f"{change(point)} ({change(low)} to {change(high)})"
        word = {"good": "Lower", "bad": "Higher", "neutral": "No clear change"}[kind]
        out.append(
            f"<g><title>{title}: {summary}</title>"
            f'<rect x="{pad}" y="{cy - row_height / 2}" width="{width - 2 * pad}"'
            f' height="{row_height}" fill="transparent"/>'
            f'<text class="strong" x="{pad}" y="{cy + 4}">{title}</text>'
            f'<line class="{kind}" x1="{x(low):.1f}" y1="{cy}" x2="{x(high):.1f}"'
            f' y2="{cy}" stroke-width="2" stroke-linecap="round"/>'
            f'<circle class="{kind}" cx="{x(point):.1f}" cy="{cy}" r="6"/>'
            f'<text x="{left + plot_width + 24}" y="{cy + 4}">{summary}</text>'
            f'<text class="{kind}-text strong" x="{left + plot_width + 180}" y="{cy + 4}">'
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
                (pa, na), (pb, nb) = passed(a), passed(b)
                text = f"{pb}/{nb}" if pa * nb == pb * na else f"{pa}/{na} → {pb}/{nb}"
                kind = pass_verdict(pa, na, pb, nb)
                tip = f"{base} {pa}/{na}; {candidate} {pb}/{nb}"
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


def pass_rate(runs):
    return f"{sum(result['passed'] for result in runs)}/{len(runs)}"


def status_counts(runs):
    counts = {}
    for result in runs:
        counts[result["status"]] = counts.get(result["status"], 0) + 1
    return ", ".join(f"{status} {count}" for status, count in sorted(counts.items()))


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
