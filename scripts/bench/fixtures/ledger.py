# Generates and checks the ledger benchmark tasks: a long billing module with
# many similar functions.
#
#   python3 ledger.py setup scattered|split|crlf
#   python3 ledger.py check scattered|split|crlf

import ast
import random
import subprocess
import sys
from pathlib import Path

VARIANTS = {
    # seed, function count, changed function count, line ending
    "scattered": (1, 120, 20, "\n"),
    "split": (2, 90, 0, "\n"),
    "crlf": (3, 40, 10, "\r\n"),
}
CUSTOMERS = """acme globex initech umbrella hooli vandelay stark wayne wonka tyrell
cyberdyne soylent oscorp aperture blackmesa massive gringotts dunder sterling
prestige monarch duff krusty gekko nakatomi weyland yoyodyne zorg virtucon
pendant bluth spacely cogswell rekall omni ollivander sirius veridian kwik
octan rich""".split()
HEADER = '''"""Billing rules for every customer contract.

Each contract has its own fee, discount, validation, and formatting rules,
negotiated separately, so the functions below are deliberately independent.
"""

from decimal import ROUND_HALF_UP, Decimal

CENT = Decimal("0.01")


def _cents(value):
    """Round a number to whole cents, rounding halves away from zero."""
    return float(Decimal(str(value)).quantize(CENT, rounding=ROUND_HALF_UP))


def _require(record, fields, prefix):
    """Return an error for each field missing from the record."""
    return [f"{prefix}: missing {field}" for field in fields if field not in record]
'''


def fee(name, p):
    return f'''

def {name}_late_fee(amount, days_late):
    """Return the late fee for an overdue {name} invoice.

    Invoices paid within the grace period owe nothing. After it, the fee
    accrues daily on the invoice amount and never falls below the minimum.
    """
    if days_late <= {p["grace"]}:
        return 0.0
    fee = amount * {p["rate"]} * (days_late - {p["grace"]})
    if fee < {p["minimum"]}:
        fee = {p["minimum"]}
    return _cents(fee)
'''


def discount(name, p):
    return f'''

def {name}_volume_discount(quantity, unit_price):
    """Return the {name} volume discount on an order line.

    Larger orders earn a larger share of the subtotal back.
    """
    subtotal = quantity * unit_price
    if quantity >= {p["t3"]}:
        rate = {p["r3"]}
    elif quantity >= {p["t2"]}:
        rate = {p["r2"]}
    elif quantity >= {p["t1"]}:
        rate = {p["r1"]}
    else:
        rate = 0
    return _cents(subtotal * rate)
'''


def validate(name, p):
    fields = ", ".join(f'"{field}"' for field in p["fields"])
    return f'''

def validate_{name}_order(record):
    """Return the problems with a {name} order record, or an empty list."""
    errors = _require(record, ({fields},), "{name}")
    quantity = record.get("quantity", 0)
    if quantity < {p["low"]}:
        errors.append("{name}: quantity below {p["low"]}")
    if quantity > {p["high"]}:
        errors.append("{name}: quantity above {p["high"]}")
    return errors
'''


def fmt(name, p):
    return f'''

def format_{name}_line(record):
    """Return one {name} invoice line for the monthly statement."""
    parts = [
        str(record["id"]),
        record["{p["field"]}"].upper(),
        f"{{record['amount']:.{p["places"]}f}}",
    ]
    return "{p["sep"]}".join(parts)
'''


KINDS = [fee, discount, validate, fmt]
FIELDS = ["sku", "quantity", "region", "currency", "contact", "po_number"]


def params(rng, kind):
    if kind is fee:
        return {
            "grace": rng.choice([3, 5, 7, 10, 14]),
            "rate": rng.choice([0.01, 0.015, 0.02, 0.025]),
            "minimum": rng.choice([5, 10, 15, 25]),
        }
    if kind is discount:
        t1 = rng.choice([10, 20, 25])
        return {
            "t1": t1,
            "t2": t1 * rng.choice([4, 5]),
            "t3": t1 * rng.choice([10, 20]),
            "r1": rng.choice([0.02, 0.03]),
            "r2": rng.choice([0.05, 0.06]),
            "r3": rng.choice([0.08, 0.1]),
        }
    if kind is validate:
        return {
            "fields": rng.sample(FIELDS[:4], 3),
            "low": rng.choice([1, 5, 10]),
            "high": rng.choice([500, 1000, 5000]),
        }
    return {
        "field": rng.choice(["sku", "region", "currency"]),
        "places": rng.choice([2, 3]),
        "sep": rng.choice([" | ", ";", ","]),
    }


def change(rng, kind, name, p):
    """Picks one change to a function: its new parameters and how SPEC.md says it."""
    q = dict(p)
    if kind is fee:
        what = rng.choice(["grace", "minimum", "rate"])
        if what == "grace":
            q["grace"] = p["grace"] + rng.choice([2, 4])
            text = f"`{name}_late_fee`: the grace period becomes {q['grace']} days."
        elif what == "minimum":
            q["minimum"] = p["minimum"] + 5
            text = f"`{name}_late_fee`: the minimum fee becomes {q['minimum']}."
        else:
            q["rate"] = round(p["rate"] + 0.005, 3)
            text = f"`{name}_late_fee`: the daily rate becomes {q['rate']}."
    elif kind is discount:
        what = rng.choice(["t2", "r3"])
        if what == "t2":
            q["t2"] = p["t2"] + p["t1"]
            text = (
                f"`{name}_volume_discount`: the middle tier starts at {q['t2']} units."
            )
        else:
            q["r3"] = round(p["r3"] + 0.02, 2)
            text = f"`{name}_volume_discount`: the top tier rate becomes {q['r3']}."
    elif kind is validate:
        what = rng.choice(["field", "high"])
        if what == "field":
            extra = next(f for f in FIELDS if f not in p["fields"])
            q["fields"] = p["fields"] + [extra]
            text = f'`validate_{name}_order`: also require the "{extra}" field, checked last.'
        else:
            q["high"] = p["high"] * 2
            text = f"`validate_{name}_order`: the maximum quantity becomes {q['high']}."
    else:
        what = rng.choice(["sep", "places"])
        if what == "sep":
            q["sep"] = " / " if p["sep"] != " / " else ";"
            text = f'`format_{name}_line`: join the parts with "{q["sep"]}".'
        else:
            q["places"] = 4
            text = f"`format_{name}_line`: show the amount with 4 decimal places."
    return q, text


def build(variant):
    """Returns the original source, the changed source, the changes, and each
    function's name, kind, and original and changed parameters."""
    seed, count, changed, _ = VARIANTS[variant]
    rng = random.Random(seed)
    functions = []
    names = [(kind, customer) for customer in CUSTOMERS for kind in KINDS]
    rng.shuffle(names)
    for kind, customer in names[:count]:
        p = params(rng, kind)
        functions.append([kind, customer, p, p])
    specs = []
    for index in sorted(rng.sample(range(count), changed)):
        kind, customer, p, _ = functions[index]
        q, text = change(rng, kind, customer, p)
        functions[index][3] = q
        specs.append(text)
    original = HEADER + "".join(k(c, p) for k, c, p, _ in functions)
    expected = HEADER + "".join(k(c, q) for k, c, _, q in functions)
    return original, expected, specs, functions


def function_name(kind, customer):
    return {
        fee: f"{customer}_late_fee",
        discount: f"{customer}_volume_discount",
        validate: f"validate_{customer}_order",
        fmt: f"format_{customer}_line",
    }[kind]


def probes(kind):
    """Arguments that distinguish every parameter of a function kind."""
    if kind is fee:
        return [(1000, d) for d in (0, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 14, 15, 30)] + [
            (10, 9),
            (10, 13),
        ]
    if kind is discount:
        return [(q, 2.5) for q in (1, 10, 25, 49, 50, 60, 80, 99, 100, 120, 125, 150, 200, 250, 400, 500, 1000)]
    if kind is validate:
        fields = {f: "x" for f in FIELDS} | {"quantity": 50}
        return [
            ({**fields, "quantity": q},) for q in (0, 1, 4, 5, 9, 10, 500, 501, 1000, 1001, 2000, 2001, 5000, 5001, 10001)
        ] + [({f: v for f, v in fields.items() if f != missing},) for missing in FIELDS]
    record = {"id": 7, "sku": "ab-1", "region": "eu", "currency": "usd", "amount": 12.34567}
    return [(record,)]


def namespace(source):
    scope = {}
    exec(compile(source, "<ledger>", "exec"), scope)
    return scope


def write_text(path, text, newline):
    path.write_bytes(text.replace("\n", newline).encode())


def setup(variant):
    original, _, specs, functions = build(variant)
    newline = VARIANTS[variant][3]
    workspace = Path.cwd()
    if variant == "split":
        write_text(workspace / "ledger.py", original, newline)
    else:
        write_text(workspace / "ledger.py", original, newline)
        spec = "# Contract changes\n\n" + "".join(f"- {text}\n" for text in specs)
        write_text(workspace / "SPEC.md", spec, newline)
    write_text(workspace / "test_ledger.py", tests(variant, functions), newline)
    git("init", "-q")
    git("add", "-A")
    git("-c", "user.name=bench", "-c", "user.email=bench@example.com", "commit", "-qm", "Initial")


def tests(variant, functions):
    """A unittest module that checks every function against expected values."""
    _, expected, _, _ = build(variant)
    scope = namespace(expected)
    module = "ledger"
    lines = ["import unittest", "", f"import {module}", "", "", "class LedgerTest(unittest.TestCase):"]
    for kind, customer, _, _ in functions:
        name = function_name(kind, customer)
        lines.append(f"    def test_{name}(self):")
        for args in probes(kind):
            result = scope[name](*args)
            lines.append(f"        self.assertEqual({module}.{name}(*{args!r}), {result!r})")
        lines.append("")
    lines += ["", 'if __name__ == "__main__":', "    unittest.main()", ""]
    return "\n".join(lines)


def git(*args):
    subprocess.run(["git", *args], check=True)


def segments(source):
    """Each top-level definition's source text, by name, plus the module's
    other statements in order."""
    tree = ast.parse(source)
    lines = source.splitlines(keepends=True)
    found = {}
    for node in tree.body:
        start = node.lineno - 1
        if getattr(node, "decorator_list", None):
            start = node.decorator_list[0].lineno - 1
        text = "".join(lines[start : node.end_lineno])
        key = getattr(node, "name", None) or ast.dump(node)
        found[key] = text
    return found


def fail(message):
    print(f"FAIL: {message}")
    raise SystemExit(1)


def check(variant):
    original, expected, _, functions = build(variant)
    newline = VARIANTS[variant][3]
    workspace = Path.cwd()
    if variant == "split":
        return check_split(original, functions)
    raw = (workspace / "ledger.py").read_bytes()
    if newline == "\r\n" and raw.replace(b"\r\n", b"").count(b"\n"):
        fail("ledger.py has lines that do not end in CRLF")
    text = raw.decode().replace("\r\n", "\n")
    if text == expected:
        print("ledger.py matches the expected file exactly")
    # Every definition that the spec leaves alone must be unchanged, and every
    # changed function must behave as specified.
    want, got = segments(expected), segments(text)
    for key, segment in want.items():
        if key not in got:
            fail(f"{key[:60]} is missing")
    changed = {function_name(k, c) for k, c, p, q in functions if p != q}
    for key, segment in want.items():
        if key not in changed and got[key] != segment:
            fail(f"{key[:60]} changed although the spec leaves it alone")
    if set(got) - set(want):
        fail(f"unexpected definitions: {sorted(set(got) - set(want))[:5]}")
    for path in workspace.iterdir():
        if path.name not in {".git", "ledger.py", "SPEC.md", "test_ledger.py", "__pycache__"}:
            fail(f"unexpected file {path.name}")
    run_tests(variant, functions)


def check_split(original, functions):
    workspace = Path.cwd()
    if (workspace / "ledger.py").exists():
        fail("ledger.py still exists")
    package = workspace / "ledger"
    homes = {fee: "fees", discount: "discounts", validate: "validation", fmt: "formatting"}
    want = segments(original)
    for kind, customer, _, _ in functions:
        name = function_name(kind, customer)
        path = package / f"{homes[kind]}.py"
        if not path.exists():
            fail(f"{path.name} is missing")
        got = segments(path.read_text())
        if got.get(name) != want[name]:
            fail(f"{name} is not unchanged in {path.name}")
    for helper in ("_cents", "_require"):
        if segments((package / "_common.py").read_text()).get(helper) != want[helper]:
            fail(f"{helper} is not unchanged in _common.py")
    for path in package.glob("*.py"):
        if path.name not in {"__init__.py", "_common.py", *(f"{h}.py" for h in homes.values())}:
            fail(f"unexpected module {path.name}")
    run_tests("split", functions)


def run_tests(variant, functions):
    # The check's own copy of the tests, so edits to the workspace's copy do
    # not count.
    check_dir = Path("/tmp/ledger-check")
    check_dir.mkdir(exist_ok=True)
    (check_dir / "test_ledger.py").write_text(tests(variant, functions))
    result = subprocess.run(
        [sys.executable, "-m", "unittest", "-q", "test_ledger"],
        cwd=check_dir,
        env={"PYTHONPATH": str(Path.cwd()), "PATH": "/usr/bin:/bin"},
    )
    if result.returncode:
        fail("tests failed")
    print("PASS")


if __name__ == "__main__":
    command, variant = sys.argv[1:]
    {"setup": setup, "check": check}[command](variant)
