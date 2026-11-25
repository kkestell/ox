# Reviewing pull requests

`AGENTS.md` holds Ox's rules, and `cmd/ox/web/AGENTS.md` adds the web
client's. Review a pull request against them and against
`docs/functional-requirements.md`, citing a requirement by its title.

Each pull request has already been through a plan and a review, recorded in
`agents/plans/` and `agents/reviews/`. The plan states the change's intent.
Read these records for context; review only the code, tests, and documents.

Report a finding only when the code shows it: a defect that happens in normal
use, a requirement the change breaks, or a rule from `AGENTS.md` the change
breaks. Name the file and line and the behavior that goes wrong. Leave out
hypothetical failures, suggested retries, fallbacks, or defensive checks for
cases that cannot occur, and formatting, which `make lint` and dprint check.
