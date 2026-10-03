# Generates and checks the routes benchmark task: a C table of many
# near-identical route entries, a few of which change.
#
#   python3 routes.py setup|check

import random
import subprocess
import sys
from pathlib import Path

RESOURCES = """accounts invoices payments refunds customers orders shipments carriers
products prices coupons taxes webhooks events reports exports""".split()
SUFFIXES = ["", "-export", "-export-v2", "-search"]
HEADER = """/* Route table for the public API gateway. */

#include "routes.h"

const struct route routes[] = {
"""
FOOTER = """};

const unsigned route_count = sizeof routes / sizeof routes[0];
"""
H = """#ifndef ROUTES_H
#define ROUTES_H

enum auth { AUTH_NONE, AUTH_TOKEN, AUTH_ADMIN };

struct route {
    const char *name;
    const char *method;
    const char *path;
    unsigned timeout_ms;
    unsigned retries;
    enum auth auth;
    unsigned rate_limit;
};

extern const struct route routes[];
extern const unsigned route_count;

#endif
"""


def entry(r):
    return f"""    {{
        .name = "{r["name"]}",
        .method = "{r["method"]}",
        .path = "{r["path"]}",
        .timeout_ms = {r["timeout_ms"]},
        .retries = {r["retries"]},
        .auth = {r["auth"]},
        .rate_limit = {r["rate_limit"]},
    }},
"""


def build():
    rng = random.Random(4)
    routes = []
    for resource in RESOURCES:
        for suffix in SUFFIXES:
            name = resource + suffix
            routes.append(
                {
                    "name": name,
                    "method": "POST" if "export" in suffix else "GET",
                    "path": f"/v1/{resource}" + ("/" + suffix[1:].replace("-", "/") if suffix else ""),
                    "timeout_ms": 3000,
                    "retries": 2,
                    "auth": "AUTH_TOKEN",
                    "rate_limit": 100,
                }
            )
    changes = []
    expected = [dict(r) for r in routes]
    for index in sorted(rng.sample(range(len(routes)), 14)):
        r = expected[index]
        field = rng.choice(["timeout_ms", "retries", "auth", "rate_limit", "two"])
        if field == "timeout_ms":
            r["timeout_ms"] = rng.choice([1500, 10000, 30000])
            text = f"timeout_ms becomes {r['timeout_ms']}"
        elif field == "retries":
            r["retries"] = rng.choice([0, 5])
            text = f"retries becomes {r['retries']}"
        elif field == "auth":
            r["auth"] = "AUTH_ADMIN"
            text = "auth becomes AUTH_ADMIN"
        elif field == "rate_limit":
            r["rate_limit"] = rng.choice([10, 25, 1000])
            text = f"rate_limit becomes {r['rate_limit']}"
        else:
            r["timeout_ms"], r["retries"] = 60000, 0
            text = "timeout_ms becomes 60000 and retries becomes 0"
        changes.append(f'- `{r["name"]}`: {text}.')
    source = HEADER + "".join(map(entry, routes)) + FOOTER
    target = HEADER + "".join(map(entry, expected)) + FOOTER
    return source, target, changes


def setup():
    source, _, changes = build()
    Path("routes.c").write_text(source)
    Path("routes.h").write_text(H)
    Path("CHANGES.md").write_text(
        "# Gateway changes\n\nChange only these routes, identified by name:\n\n"
        + "\n".join(changes)
        + "\n"
    )
    for args in (["init", "-q"], ["add", "-A"]):
        subprocess.run(["git", *args], check=True)
    subprocess.run(
        ["git", "-c", "user.name=bench", "-c", "user.email=bench@example.com", "commit", "-qm", "Initial"],
        check=True,
    )


def check():
    _, target, _ = build()
    got = Path("routes.c").read_text()
    if got != target:
        expected, actual = target.splitlines(), got.splitlines()
        wrong = [i + 1 for i, (a, b) in enumerate(zip(expected, actual)) if a != b]
        print(f"FAIL: routes.c differs from the expected file; first differing lines {wrong[:10]},"
              f" {len(actual)} lines instead of {len(expected)}")
        raise SystemExit(1)
    if Path("routes.h").read_text() != H:
        print("FAIL: routes.h changed")
        raise SystemExit(1)
    subprocess.run(["cc", "-Wall", "-Wextra", "-Werror", "-c", "routes.c", "-o", "/tmp/routes.o"], check=True)
    print("PASS")


if __name__ == "__main__":
    {"setup": setup, "check": check}[sys.argv[1]]()
