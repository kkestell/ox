# Known issues

| Issue | Frequency in real use | Confirmed |
| --- | --- | --- |
| [An unknown effort level is ignored](#an-unknown-effort-level-is-ignored) | Unknown | Live |
| [A failed configuration can leave settings the page does not show](#a-failed-configuration-can-leave-settings-the-page-does-not-show) | Unknown | Source |

## An unknown effort level is ignored

Claude starts with a misspelled effort from the configuration and runs at
its [default effort](claude-code.md#initialize-first) without an error. The
page shows the configured value, so prompts can run at a different effort and
cost than shown. Confirmed live with the `--effort` launch flag.

## A failed configuration can leave settings the page does not show

While applying [`session/configure`](protocol.md#sessionconfigure), Claude
can accept a change before refusing a later one. A database write can fail
after Claude accepts all changes. In either case the RPC returns an error
and the page shows the old settings while the running process keeps the
changes it accepted. The web form sends two settings together only when a
model change also changes effort, and the built-in models all offer the same
efforts. Which efforts Claude refuses through a control request is untested.

Until a successful configuration reconciles the settings or the process
restarts, prompts can use a different effort, a costlier model, or (after a
failed database write) a permission mode other than the one shown.
