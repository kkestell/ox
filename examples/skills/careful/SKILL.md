---
name: careful
description: Work on a request with the Git status up front, destructive shell commands denied, and patches checked.
argument-hint: "<request>"
hooks:
  before_run:
    command: python3 scripts/careful.py
  before_tool:
    command: python3 scripts/careful.py
  after_tools:
    command: python3 scripts/careful.py
  after_run:
    command: python3 scripts/careful.py
---

Work on the request given as arguments.

The first feedback message shows the workspace's Git status before any change.
Use it to see what was already modified.

Shell commands that can destroy work, such as `rm -rf`, `git reset --hard`,
`git clean -f`, and `git push`, are denied, whether they run in the foreground,
start in the background, or are written as input to a running shell process.
Use a reversible alternative, or stop and ask the user when the destructive
command is required.

After each batch of patches, feedback reports whitespace errors in the changed
tracked files. Fix them before finishing. Finish with a short answer that
states what changed.
