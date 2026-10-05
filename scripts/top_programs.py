#!/usr/bin/env python3
# Prints, as a JSON object, the number of Claude Code shell commands that use
# each program. Shell builtins, POSIX utilities, and programs not on $PATH are
# left out. Sessions come from $CLAUDE_CONFIG_DIR/projects, else
# ~/.claude/projects. --combine reads several people's JSON files and prints the
# top 50 programs with their total count and the number of people who use them.
#   scripts/top_programs.py > kyle.json
#   scripts/top_programs.py --combine *.json

import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
from collections import Counter
from pathlib import Path

# The utilities in POSIX.1-2017, Shell and Utilities, chapter 4.
POSIX = set("""
admin alias ar asa at awk basename batch bc bg c99 cal cat cd cflow chgrp chmod
chown cksum cmp comm command compress cp crontab csplit ctags cut cxref date dd
delta df diff dirname du echo ed env ex expand expr false fc fg file find fold
fort77 fuser gencat get getconf getopts grep hash head iconv id ipcrm ipcs jobs
join kill lex link ln locale localedef logger logname lp ls m4 mailx make man
mesg mkdir mkfifo more mv newgrp nice nl nm nohup od paste patch pathchk pax pr
printf prs ps pwd qalter qdel qhold qmove qmsg qrerun qrls qselect qsig qstat
qsub read renice rm rmdel rmdir sact sccs sed sh sleep sort split strings strip
stty tabs tail talk tee test time touch tput tr true tsort tty type ulimit umask
unalias uname uncompress unexpand unget uniq unlink uucp uudecode uuencode
uustat uux val vi wait wc what who write xargs yacc zcat
""".split())
# Bash builtins and reserved words, such as source, export, for, and [[.
SHELL = set(subprocess.run(["bash", "-c", "compgen -b; compgen -k"],
                           capture_output=True, text=True, check=True).stdout.split())
# Programs that run another program named later in the command.
WRAPPERS = {"sudo", "time", "env", "command", "exec", "nohup", "xargs", "timeout"}
HEREDOC = re.compile(r"<<-?\s*['\"]?(\w+)['\"]?(.*?)\n.*?\n\s*\1\s*(?=\n|$)", re.S)
QUOTED = re.compile(r"'[^']*'|\"(?:[^\"\\]|\\.)*\"|\$\([^)]*\)", re.S)
SEPARATOR = re.compile(r"&&|\|\||[;|\n]")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--combine", nargs="+", type=Path, metavar="FILE")
    args = parser.parse_args()
    if args.combine:
        combine(args.combine)
    else:
        count()


def count():
    root = Path(os.environ.get("CLAUDE_CONFIG_DIR", Path.home() / ".claude"))
    counts = Counter()
    for path in (root / "projects").rglob("*.jsonl"):
        for line in path.open(errors="replace"):
            try:
                content = json.loads(line).get("message", {}).get("content")
            except (json.JSONDecodeError, AttributeError):
                continue
            for block in content if isinstance(content, list) else []:
                if (isinstance(block, dict) and block.get("type") == "tool_use"
                        and block.get("name") == "Bash"):
                    counts.update(programs(block.get("input", {}).get("command") or ""))
    found = {name: n for name, n in counts.most_common() if shutil.which(name)}
    print(json.dumps(found, indent=2))


def combine(paths):
    total, people = Counter(), Counter()
    for path in paths:
        counts = json.loads(path.read_text())
        total.update(counts)
        people.update(counts.keys())
    print("  total  people  program")
    for name, n in total.most_common(50):
        print(f"{n:7d}  {people[name]:6d}  {name}")


def programs(command):
    # Heredoc bodies and quoted text are data, not commands.
    command = QUOTED.sub(" _ ", HEREDOC.sub(r"\2", command))
    found = set()
    for segment in SEPARATOR.split(command):
        try:
            words = shlex.split(segment, comments=True)
        except ValueError:
            words = segment.split()
        while words and (re.match(r"\w+=", words[0]) or words[0] in WRAPPERS
                         or words[0] in "({!" or words[0].startswith("-")):
            words = words[1:]
        if not words:
            continue
        name = words[0].lstrip("({").rsplit("/", 1)[-1]
        if (re.fullmatch(r"[A-Za-z][\w.+-]*", name)
                and name not in POSIX and name not in SHELL):
            found.add(name)
    return found


if __name__ == "__main__":
    main()
