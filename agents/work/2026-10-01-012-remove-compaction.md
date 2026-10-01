# Remove compaction

## Plan

`agents/plans/2026-10-01-012-remove-compaction.md`

## Summary

Compaction, the `/compact` command, summarizer requests, and compaction
checkpoints are gone. A provider's input context overflow panics with the
planned message. The OpenAI stream parser builds the completion from completed
items only. OX-0045, OX-0046, and OX-0048 are fixed; OX-0047 is won't fix. The
plan's goal is met.

## Departures from the plan

- `crates/ox/tests/tui.rs`: `render_replies` now picks the two replies after the
  tool calls by requester, through `Reply::from`. The fixture serves replies in
  arrival order, and the main agent and its subagent request concurrently.
  Compaction's request size estimate had delayed the main agent enough that the
  subagent always asked first. Without it,
  `the_transcript_view_renders_thinking_tools_and_wrapped_replies` failed in
  three of four `make e2e` runs. `HEAD` passed three of three.

## Decisions

- `openai::base_body` was inlined into `openai::ordinary_body`, its only
  remaining caller.
- `ToolStatus::id`, used only by compaction's action logs, was deleted.
- `user_message` in `openrouter.rs` and `openai.rs` became private, since
  `Provider::user_message` no longer calls them.

## Test changes

- Gained: `an_input_context_overflow_panics_with_a_clear_message` owns the
  overflow panic.
- Lost, with the feature: compaction, summarizer, checkpoint validation, request
  admission, and summarizer cost guarantees.
- Unchanged owners: image admission stays with
  `invalid_prompt_startup_sends_no_request_or_user_message`, and OpenAI overflow
  classification stays with
  `refusal_context_overflow_truncation_and_stalls_have_distinct_outcomes`.

## Automated checks

- `make check` — Passed.
- `make e2e` — Passed five runs in a row after the fixture change.

## Manual verification

1. A live OpenRouter prompt runs a tool and answers. `scripts/run.py` reads
   `~/.config/ox/settings.json`, whose unqualified model IDs this build rejects,
   so the check used an isolated home with the example settings.

   ```sh
   home=$(mktemp -d) ws=$(mktemp -d)
   mkdir -p $home/.config/ox && cp examples/settings.json $home/.config/ox/
   touch $ws/a.txt $ws/b.txt
   set -a && . ./.env && set +a
   HOME=$home target/debug/ox run --dir $ws \
     --model openrouter:deepseek/deepseek-v4.1-flash \
     'Run `ls` and tell me how many files there are.'
   ```

   Observed: "There are **2 files**: `a.txt` and `b.txt`."

2. Deleted `~/.local/share/ox/ox.db`, `ox.db-shm`, and `ox.db-wal`.

## Follow-up work

- `~/.config/ox/settings.json` uses unqualified model IDs and fails to load.
- `AGENTS.md` says the local install targets recreate the database, but the
  `Makefile` install targets do not.
