#!/usr/bin/env python3
# Runs the benchmark tasks in scripts/bench/tasks.toml against one build of ox,
# each repetition in its own Docker container, and compares labeled benchmark
# runs.
#
#   scripts/bench.py run --label base --ref main --model MODEL_ID --effort LEVEL
#   scripts/bench.py run --label change --model MODEL_ID --effort LEVEL
#   scripts/bench.py compare base change

import argparse
import concurrent.futures
import datetime
import json
import os
import re
import sqlite3
import statistics
import subprocess
import time
import tomllib
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BENCH = ROOT / "bench"
TASKS = ROOT / "scripts" / "bench" / "tasks.toml"
IMAGE = "ox-bench"
# Seconds between SIGTERM and SIGKILL for a timed-out run.
KILL_GRACE = 30

METRICS = [
    "passed",
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
    "compactions",
    "summarizer_cost",
    "subagents",
]


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
    run_parser.add_argument("--timeout", type=int, default=900, metavar="SECONDS")
    run_parser.add_argument("--task", nargs="+", dest="tasks", metavar="ID")
    compare_parser = commands.add_parser("compare")
    compare_parser.add_argument("labels", nargs="+", metavar="LABEL")
    args = parser.parse_args()
    if args.command == "run":
        run(parser, args)
    else:
        compare(parser, args.labels)


def run(parser, args):
    if not re.fullmatch(r"[A-Za-z0-9._-]+", args.label):
        parser.error("--label may contain only letters, digits, '.', '_', and '-'")
    label_dir = BENCH / "runs" / args.label
    if label_dir.exists():
        parser.error(f"{label_dir} already exists")
    tasks = load_tasks()
    if args.tasks:
        unknown = set(args.tasks) - {task["id"] for task in tasks}
        if unknown:
            parser.error(f"unknown tasks: {', '.join(sorted(unknown))}")
        tasks = [task for task in tasks if task["id"] in args.tasks]

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
    }
    label_dir.mkdir(parents=True)
    runs = [(task, rep) for rep in range(1, args.reps + 1) for task in tasks]
    with concurrent.futures.ThreadPoolExecutor(args.jobs) as pool:
        futures = [
            pool.submit(run_repetition, binary, api_key, args, identity, task, rep)
            for task, rep in runs
        ]
        try:
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                print(
                    f"{result['task']} {result['rep']}: {result['status']},"
                    f" {'passed' if result['passed'] else 'failed check'},"
                    f" {result['requests']} requests,"
                    f" {result['tool_calls']} tool calls,"
                    f" ${result['cost']:.4f}, {result['seconds']:.0f}s",
                    flush=True,
                )
        except KeyboardInterrupt:
            # Removing the containers ends the running `docker exec` calls, so
            # the pool's threads finish instead of waiting on `sleep infinity`.
            pool.shutdown(wait=False, cancel_futures=True)
            containers = docker(
                "ps", "-aq", "--filter", f"label=ox-bench={args.label}"
            ).split()
            if containers:
                docker("rm", "-f", *containers)
            raise SystemExit(130)


def load_tasks():
    tasks = tomllib.loads(TASKS.read_text())["task"]
    for task in tasks:
        if "repo" in task:
            match = re.fullmatch(
                r"https://github\.com/([^/]+)/([^/]+)/commit/([0-9a-fA-F]+)",
                task["repo"].rstrip("/"),
            )
            if not match:
                raise SystemExit(f"{task['id']}: repo must be a GitHub commit URL")
            owner, name, commit = match.groups()
            task["clone"] = (f"https://github.com/{owner}/{name}.git", commit)
    return tasks


def git(*args, cwd=ROOT):
    return subprocess.run(
        ["git", *args], cwd=cwd, check=True, capture_output=True, text=True
    ).stdout.strip()


def docker(*args):
    return subprocess.run(
        ["docker", *args], check=True, capture_output=True, text=True
    ).stdout.strip()


def build(ref, label):
    """Builds ox and returns the binary, its commit, and whether the tree was dirty."""
    subprocess.run(["docker", "build", "-t", IMAGE, str(TASKS.parent)], check=True)
    bin_dir = BENCH / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    if ref:
        commit = git("rev-parse", "--verify", f"{ref}^{{commit}}")
        dirty = False
        binary = bin_dir / f"ox-{commit}"
        if binary.exists():
            print(f"reusing {binary}", flush=True)
            return binary, commit, dirty
        worktree = BENCH / "worktree" / label
        git("worktree", "add", "--detach", str(worktree), commit)
        try:
            cargo_build(worktree, binary, label)
        finally:
            git("worktree", "remove", "--force", str(worktree))
    else:
        commit = git("rev-parse", "HEAD")
        dirty = bool(git("status", "--porcelain", "--untracked-files=no"))
        binary = bin_dir / f"ox-{label}"
        cargo_build(ROOT, binary, label)
    return binary, commit, dirty


def cargo_build(source, binary, label):
    # Every build shares one target directory and one Cargo registry in Docker
    # volumes. Cargo compares file times, not content, to decide whether Ox's
    # crates are fresh, so a build cleans them first, holding a lock so that no
    # other build replaces them before the binary is copied.
    partial = f"{binary.name}.tmp-{label}"
    script = (
        "cargo clean --profile fast -p ox -p ox-server"
        " && cargo build --profile fast -p ox"
        f" && cp /target/fast/ox /out/{partial}"
    )
    subprocess.run(
        [
            "docker",
            "run",
            "--rm",
            "-v",
            f"{source}:/src:ro",
            "-v",
            "ox-bench-target:/target",
            "-v",
            "ox-bench-cargo:/usr/local/cargo/registry",
            "-v",
            f"{binary.parent}:/out",
            "-w",
            "/src",
            "-e",
            "CARGO_TARGET_DIR=/target",
            IMAGE,
            "flock",
            "/target/bench.lock",
            "sh",
            "-c",
            script,
        ],
        check=True,
    )
    # Docker Desktop's file sharing can create the file without execute
    # permission. A concurrent benchmark run that finds the binary never reads a
    # partial file.
    os.chmod(binary.parent / partial, 0o755)
    os.replace(binary.parent / partial, binary)


def run_repetition(binary, api_key, args, identity, task, rep):
    run_dir = BENCH / "runs" / args.label / task["id"] / str(rep)
    run_dir.mkdir(parents=True)
    container = f"ox-bench-{args.label}-{task['id']}-{rep}"
    docker(
        "run",
        "-d",
        "--name",
        container,
        "--label",
        f"ox-bench={args.label}",
        IMAGE,
        "sleep",
        "infinity",
    )
    try:
        docker("cp", str(binary), f"{container}:/usr/local/bin/ox")
        docker("exec", container, "mkdir", "/workspace", "/data")
        if "clone" in task:
            url, commit = task["clone"]
            docker("exec", container, "git", "clone", "--quiet", url, "/workspace")
            docker(
                "exec",
                "-w",
                "/workspace",
                container,
                "git",
                "checkout",
                "--quiet",
                commit,
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
                    str(args.timeout),
                    "ox",
                    "run",
                    "--dir",
                    "/workspace",
                    "--model",
                    args.model,
                    "--effort",
                    args.effort,
                    task["prompt"],
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

        with open(run_dir / "check.txt", "w") as check:
            passed = (
                subprocess.run(
                    [
                        "docker",
                        "exec",
                        "-w",
                        "/workspace",
                        container,
                        "sh",
                        "-c",
                        task["check"],
                    ],
                    stdin=subprocess.DEVNULL,
                    stdout=check,
                    stderr=subprocess.STDOUT,
                ).returncode
                == 0
            )

        docker("cp", f"{container}:/workspace", str(run_dir / "workspace"))
        docker("cp", f"{container}:/data", str(run_dir / "data"))
    finally:
        docker("rm", "-f", container)

    result = identity | {
        "task": task["id"],
        "rep": rep,
        "started_at": started_at,
        "passed": passed,
        "status": status,
        "seconds": round(seconds, 1),
    }
    result |= transcript_metrics(run_dir / "data" / "ox.db")
    (run_dir / "result.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


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
        "compactions": 0,
        "summarizer_cost": 0.0,
        "subagents": 0,
    }
    # Ox never opened its database when it failed before creating a session.
    if not database.exists():
        return metrics
    connection = sqlite3.connect(f"file:{database}?mode=ro", uri=True)
    try:
        metrics["subagents"] = connection.execute(
            "SELECT count(*) FROM sessions WHERE parent_session_id IS NOT NULL"
        ).fetchone()[0]
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
                metrics["cost"] += usage["cost"]
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
        elif kind == "compaction_checkpoint":
            metrics["compactions"] += 1
            metrics["summarizer_cost"] += entry["summarizer_cost"] or 0.0
    metrics["cost"] = round(metrics["cost"], 6)
    metrics["summarizer_cost"] = round(metrics["summarizer_cost"], 6)
    return metrics


def compare(parser, labels):
    results = {}
    for label in labels:
        paths = sorted((BENCH / "runs" / label).glob("*/*/result.json"))
        if not paths:
            parser.error(f"no results for {label}")
        results[label] = [json.loads(path.read_text()) for path in paths]

    header = ["label", "commit", "dirty", "model", "effort", "results"]
    rows = []
    for label, label_results in results.items():
        first = label_results[0]
        rows.append(
            [
                label,
                first["commit"][:10],
                "yes" if first["dirty"] else "no",
                first["model"],
                first["effort"],
                str(len(label_results)),
            ]
        )
    print_table(header, rows)

    task_ids = sorted({result["task"] for runs in results.values() for result in runs})
    totals = {label: {} for label in labels}
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
                    ["passed", *(pass_rate(runs) for runs in by_label.values())]
                )
                for label, runs in by_label.items():
                    passed, count = totals[label].get("passed", (0, 0))
                    totals[label]["passed"] = (
                        passed + sum(result["passed"] for result in runs),
                        count + len(runs),
                    )
            elif metric == "status":
                rows.append(
                    ["status", *(status_counts(runs) for runs in by_label.values())]
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
                        rows.append(numeric_row(f"{name} {field}", "", values, totals))
            else:
                values = {
                    label: [result[metric] for result in runs]
                    for label, runs in by_label.items()
                }
                rows.append(numeric_row(metric, metric, values, totals))
        print(f"\n{task_id}")
        print_table(["metric", *labels], rows)

    rows = [
        [
            "passed",
            *(
                f"{totals[label]['passed'][0]}/{totals[label]['passed'][1]}"
                for label in labels
            ),
        ]
    ]
    for metric in METRICS:
        if metric in ("passed", "status", "calls_by_tool"):
            continue
        sums = [totals[label].get(metric, 0) for label in labels]
        rows.append(
            [metric, *with_changes(sums, [number(metric, value) for value in sums])]
        )
    print("\ntotals (sums of task medians)")
    print_table(["metric", *labels], rows)


def pass_rate(runs):
    return f"{sum(result['passed'] for result in runs)}/{len(runs)}"


def status_counts(runs):
    counts = {}
    for result in runs:
        counts[result["status"]] = counts.get(result["status"], 0) + 1
    return " ".join(f"{status} {count}" for status, count in sorted(counts.items()))


def numeric_row(name, metric, values, totals):
    """One row of medians with ranges, adding each median to the label's total."""
    medians = []
    cells = []
    for label, label_values in values.items():
        if not label_values:
            medians.append(None)
            cells.append("-")
            continue
        median = statistics.median(label_values)
        medians.append(median)
        if metric:
            totals[label][metric] = totals[label].get(metric, 0) + median
        low, high = min(label_values), max(label_values)
        cell = number(metric, median)
        if low != high:
            cell += f" ({number(metric, low)}–{number(metric, high)})"
        cells.append(cell)
    return [name, *with_changes(medians, cells)]


def with_changes(values, cells):
    """Appends the percentage change from the first value to each later cell."""
    base = values[0]
    changed = [cells[0]]
    for value, cell in zip(values[1:], cells[1:]):
        if base and value is not None:
            cell += f" {(value - base) / base * 100:+.0f}%"
        changed.append(cell)
    return changed


def number(metric, value):
    if metric in ("cost", "summarizer_cost"):
        return f"${value:.4f}"
    if metric == "seconds" or float(value).is_integer():
        return f"{value:.0f}"
    return f"{value:.1f}"


def print_table(header, rows):
    widths = [
        max(len(row[column]) for row in [header, *rows])
        for column in range(len(header))
    ]
    for row in [header, *rows]:
        print(
            "  ".join(
                cell.ljust(width) if column == 0 else cell.rjust(width)
                for column, (cell, width) in enumerate(zip(row, widths))
            ).rstrip()
        )


raise SystemExit(main())
