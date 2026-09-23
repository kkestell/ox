---
name: goal
description: Work toward an objective until a judge accepts the result.
argument-hint: "<objective>"
hooks:
  before_stop:
    command: python3 scripts/judge.py
---

Work toward the objective given as arguments until it is met.

Break the objective into concrete steps and complete them in the workspace.
Verify the result before finishing: run the relevant builds, tests, or
commands, and inspect their output. Finish with a short answer that states what
changed and how you verified it.

A judge reviews each finished answer against the objective. When its feedback
says work remains, address that feedback and finish again. When you cannot
continue without the user, say exactly what you need from them.
