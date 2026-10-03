# Generates and checks the legacy benchmark tasks: C projects whose files use
# Windows line endings or Latin-1 text, which edits must preserve, and the same
# project with Unix line endings for comparison.
#
#   python3 legacy.py setup|check crlf|lf|latin1

import subprocess
import sys
from pathlib import Path

UNITS_H = """#ifndef UNITS_H
#define UNITS_H

/* Temperature conversions. Every function takes degrees Celsius. */

double units_to_fahrenheit(double celsius);
double units_to_rankine(double celsius);

/* Returns the unit for a letter, or 0 when the letter names no unit. */
char units_parse(const char *text);

#endif
"""
UNITS_C = """#include <string.h>

#include "units.h"

double units_to_fahrenheit(double celsius)
{
    return celsius * 9.0 / 5.0 + 32.0;
}

double units_to_rankine(double celsius)
{
    return (celsius + 273.15) * 9.0 / 5.0;
}

char units_parse(const char *text)
{
    if (strcmp(text, "F") == 0 || strcmp(text, "R") == 0) {
        return text[0];
    }
    return 0;
}
"""
CONVERT_C = """#include <stdio.h>
#include <stdlib.h>

#include "units.h"

static int usage(void)
{
    fprintf(stderr, "usage: convert CELSIUS F|R\\n");
    return 2;
}

int main(int argc, char **argv)
{
    if (argc != 3) {
        return usage();
    }
    char *end;
    double celsius = strtod(argv[1], &end);
    char unit = units_parse(argv[2]);
    if (*end != '\\0' || unit == 0) {
        return usage();
    }
    double value = unit == 'F' ? units_to_fahrenheit(celsius) : units_to_rankine(celsius);
    printf("%.2f %c\\n", value, unit);
    return 0;
}
"""
README = """convert
=======

Converts a temperature in degrees Celsius to another unit.

    convert CELSIUS F|R

F is Fahrenheit and R is Rankine. The result has two decimal places.
"""
MAKEFILE = """CFLAGS = -std=c99 -Wall -Wextra -Werror

convert: convert.c units.c units.h
\t$(CC) $(CFLAGS) -o $@ convert.c units.c
"""
CRLF_SPEC = """# Kelvin support

1. Declare `double units_to_kelvin(double celsius);` in units.h after
   units_to_rankine, and define it in units.c.
2. Make units_parse accept "K", and make convert print Kelvin for K.
3. Print results with three decimal places instead of two.
4. Update the usage message and README.txt to list K as Kelvin.
"""
MESSAGES_C = """/* Messages du programme, en français. Fichier encodé en ISO-8859-1. */

#include <stdio.h>
#include <string.h>

static const char *messages[][2] = {
    {"open", "Fichier introuvable : %s"},
    {"read", "Erreur de lecture à la ligne %s"},
    {"done", "Opération terminée"},
    {"empty", "Le fichier est vide"},
};

int main(int argc, char **argv)
{
    if (argc < 2) {
        fprintf(stderr, "usage : messages CLÉ [ARGUMENT]\\n");
        return 2;
    }
    for (size_t i = 0; i < sizeof messages / sizeof messages[0]; i++) {
        if (strcmp(argv[1], messages[i][0]) == 0) {
            printf(messages[i][1], argc > 2 ? argv[2] : "");
            printf("\\n");
            return 0;
        }
    }
    fprintf(stderr, "clé inconnue : %s\\n", argv[1]);
    return 1;
}
"""
LATIN1_SPEC = """# Nouveaux messages

1. Change the "done" message to "Opération réussie".
2. Add a "write" message, "Échec de l'écriture dans %s", after "read".
3. Add a "perm" message, "Accès refusé à %s", at the end of the table.
"""


def setup(variant):
    if variant in ("crlf", "lf"):
        newline = "\r\n" if variant == "crlf" else "\n"
        files = {"units.h": UNITS_H, "units.c": UNITS_C, "convert.c": CONVERT_C, "README.txt": README}
        for name, text in files.items():
            Path(name).write_bytes(text.replace("\n", newline).encode())
        Path("Makefile").write_text(MAKEFILE)
        Path("SPEC.md").write_text(CRLF_SPEC)
    else:
        Path("messages.c").write_bytes(MESSAGES_C.encode("latin-1"))
        Path("Makefile").write_text("CFLAGS = -std=c99 -Wall -Wextra -Werror\n\nmessages: messages.c\n")
        Path("SPEC.md").write_text(LATIN1_SPEC)
    for args in (["init", "-q"], ["add", "-A"]):
        subprocess.run(["git", *args], check=True)
    subprocess.run(
        ["git", "-c", "user.name=bench", "-c", "user.email=bench@example.com", "commit", "-qm", "Initial"],
        check=True,
    )


def fail(message):
    print(f"FAIL: {message}")
    raise SystemExit(1)


def output(*args):
    result = subprocess.run(args, capture_output=True)
    return result.returncode, result.stdout


def check(variant):
    subprocess.run(["make", "-B"], check=True)
    if variant in ("crlf", "lf"):
        for name in ("units.h", "units.c", "convert.c", "README.txt"):
            data = Path(name).read_bytes()
            if variant == "crlf" and (
                data.replace(b"\r\n", b"").count(b"\n") or not data.endswith(b"\r\n")
            ):
                fail(f"{name} has lines that do not end in CRLF")
            if variant == "lf" and b"\r" in data:
                fail(f"{name} has carriage returns")
        if b"Kelvin" not in Path("README.txt").read_bytes():
            fail("README.txt does not mention Kelvin")
        for args, want in [
            (("100", "K"), (0, b"373.150 K\n")),
            (("100", "F"), (0, b"212.000 F\n")),
            (("0", "R"), (0, b"491.670 R\n")),
            (("1", "X"), (2, b"")),
        ]:
            if output("./convert", *args) != want:
                fail(f"convert {' '.join(args)} printed {output('./convert', *args)}")
        if "units_to_kelvin" not in Path("units.h").read_text():
            fail("units.h does not declare units_to_kelvin")
    else:
        data = Path("messages.c").read_bytes()
        try:
            data.decode("utf-8")
            fail("messages.c is no longer ISO-8859-1")
        except UnicodeDecodeError:
            pass
        for line in MESSAGES_C.encode("latin-1").splitlines():
            if b'"done"' not in line and line not in data.splitlines():
                fail(f"messages.c lost the line {line!r}")
        for args, want in [
            (("done",), "Opération réussie\n"),
            (("write", "a.txt"), "Échec de l'écriture dans a.txt\n"),
            (("perm", "b"), "Accès refusé à b\n"),
            (("read", "3"), "Erreur de lecture à la ligne 3\n"),
        ]:
            if output("./messages", *args) != (0, want.encode("latin-1")):
                fail(f"messages {' '.join(args)} printed {output('./messages', *args)}")
        order = [line.split(b'"')[1] for line in data.splitlines() if line.strip().startswith(b'{"')]
        if order != [b"open", b"read", b"write", b"done", b"empty", b"perm"]:
            fail(f"the message table is in the wrong order: {order}")
    print("PASS")


if __name__ == "__main__":
    command, variant = sys.argv[1:]
    {"setup": setup, "check": check}[command](variant)
