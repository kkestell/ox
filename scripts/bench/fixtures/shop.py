# Generates and checks the shop benchmark tasks: a small Python package that
# gains a feature across several files, with Windows or Unix line endings.
#
#   python3 shop.py setup|check crlf|lf

import subprocess
import sys
from pathlib import Path

FILES = {
    "shop/__init__.py": '''"""A tiny shop: carts, prices, and a command line."""
''',
    "shop/pricing.py": '''"""Prices, taxes, and rounding."""

from decimal import ROUND_HALF_UP, Decimal

TAX_RATES = {"US": Decimal("0.07"), "DE": Decimal("0.19"), "JP": Decimal("0.10")}


def cents(value):
    """Round to whole cents, rounding halves away from zero."""
    return Decimal(value).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)


def tax(amount, country):
    """Return the sales tax on an amount for a country."""
    if country not in TAX_RATES:
        raise ValueError(f"unknown country: {country}")
    return cents(amount * TAX_RATES[country])
''',
    "shop/cart.py": '''"""A shopping cart."""

from decimal import Decimal

from shop.pricing import cents, tax


class Cart:
    """Items in a cart, priced for one country."""

    def __init__(self, country):
        self.country = country
        self.lines = []

    def add(self, sku, quantity, unit_price):
        """Add a line; quantity must be positive."""
        if quantity <= 0:
            raise ValueError("quantity must be positive")
        self.lines.append((sku, quantity, Decimal(unit_price)))

    def subtotal(self):
        """Return the sum of every line."""
        return cents(sum((q * p for _, q, p in self.lines), Decimal(0)))

    def total(self):
        """Return the subtotal plus tax."""
        subtotal = self.subtotal()
        return subtotal + tax(subtotal, self.country)
''',
    "shop/cli.py": '''"""Prices a cart from the command line.

    python3 -m shop.cli --country US sku:quantity:price ...
"""

import argparse

from shop.cart import Cart


def main(argv=None):
    parser = argparse.ArgumentParser(prog="shop")
    parser.add_argument("--country", default="US")
    parser.add_argument("lines", nargs="+", metavar="sku:quantity:price")
    args = parser.parse_args(argv)
    cart = Cart(args.country)
    for line in args.lines:
        sku, quantity, price = line.split(":")
        cart.add(sku, int(quantity), price)
    print(f"subtotal {cart.subtotal()}")
    print(f"total {cart.total()}")


if __name__ == "__main__":
    main()
''',
    "shop/__main__.py": '''from shop.cli import main

main()
''',
    "README.txt": """shop
====

Prices a cart from the command line:

    python3 -m shop --country US apple:3:0.50 pear:1:1.25

It prints the subtotal and the total with tax.
""",
}
SPEC = """# Coupons

1. In shop/pricing.py, add `COUPONS = {"SAVE10": Decimal("0.10"), "HALF":
   Decimal("0.50")}` and a function `discount(amount, code)` that returns the
   discount for a coupon code, rounded to cents. It raises ValueError with the
   message `unknown coupon: CODE` for an unknown code.
2. Give Cart a `coupon` attribute, None by default, and a method
   `apply_coupon(code)` that validates the code with `discount` and stores it.
   Add a method `discount()` that returns the coupon's discount on the subtotal,
   or 0.00 without a coupon. Tax applies to the subtotal minus the discount, and
   `total()` returns the subtotal minus the discount plus that tax.
3. Add a `--coupon CODE` option to the command line. When it is given, print
   `discount AMOUNT` between the subtotal and total lines.
4. Document the option in README.txt.
"""
CHECK = '''
import subprocess, sys
from decimal import Decimal
from shop.cart import Cart
from shop.pricing import discount

assert discount(Decimal("10.00"), "SAVE10") == Decimal("1.00")
assert discount(Decimal("0.05"), "HALF") == Decimal("0.03")
try:
    discount(Decimal(1), "BOGUS")
    raise SystemExit("discount accepted BOGUS")
except ValueError as error:
    assert str(error) == "unknown coupon: BOGUS", error
cart = Cart("DE")
cart.add("a", 3, "10.00")
assert cart.coupon is None and cart.discount() == Decimal("0.00")
assert cart.total() == Decimal("35.70"), cart.total()
cart.apply_coupon("SAVE10")
assert cart.coupon == "SAVE10"
assert cart.discount() == Decimal("3.00"), cart.discount()
assert cart.total() == Decimal("32.13"), cart.total()
try:
    cart.apply_coupon("NOPE")
    raise SystemExit("apply_coupon accepted NOPE")
except ValueError:
    pass
run = lambda *a: subprocess.run([sys.executable, "-m", "shop", *a], capture_output=True, text=True).stdout
assert run("--country", "US", "a:2:5.00") == "subtotal 10.00\\ntotal 10.70\\n", run("a:2:5.00")
assert run("--coupon", "HALF", "a:2:5.00") == "subtotal 10.00\\ndiscount 5.00\\ntotal 5.35\\n", run("--coupon", "HALF", "a:2:5.00")
'''


def setup(variant):
    newline = "\r\n" if variant == "crlf" else "\n"
    for name, text in FILES.items():
        path = Path(name)
        path.parent.mkdir(exist_ok=True)
        path.write_bytes(text.replace("\n", newline).encode())
    Path("SPEC.md").write_text(SPEC)
    for args in (["init", "-q"], ["add", "-A"]):
        subprocess.run(["git", *args], check=True)
    subprocess.run(
        ["git", "-c", "user.name=bench", "-c", "user.email=bench@example.com", "commit", "-qm", "Initial"],
        check=True,
    )


def check(variant):
    for name in FILES:
        data = Path(name).read_bytes()
        if variant == "crlf" and (data.replace(b"\r\n", b"").count(b"\n") or not data.endswith(b"\r\n")):
            print(f"FAIL: {name} has lines that do not end in CRLF")
            raise SystemExit(1)
        if variant == "lf" and b"\r" in data:
            print(f"FAIL: {name} has carriage returns")
            raise SystemExit(1)
    if "--coupon" not in Path("README.txt").read_text():
        print("FAIL: README.txt does not document --coupon")
        raise SystemExit(1)
    subprocess.run([sys.executable, "-c", CHECK], check=True)
    print("PASS")


if __name__ == "__main__":
    command, variant = sys.argv[1:]
    {"setup": setup, "check": check}[command](variant)
