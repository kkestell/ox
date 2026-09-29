# Single ox binary

## Plan

`agents/plans/2026-09-29-005-single-ox-binary.md`

## Summary

Ox ships as one binary, `ox`, with the `run`, `acp`, and `auth` subcommands and
one global settings file, `~/.config/ox/settings.json`. The plan's goal is met.

## Departures from the plan

- The Ox client takes its workspace as `ox --dir <path>` instead of a positional
  directory. With a positional directory, clap parses `ox login` as the
  directory `login`, so the plan's rejected case could not hold. The user chose
  `--dir`.
- The bundled server test compares the command with `current_exe()` exactly
  instead of checking that it ends in `ox`; the test binary's name does not end
  in `ox`.

## Decisions

- The library exports `global_path` and `home_dir` instead of the whole
  `settings` module.
- `logout` returns `io::Result`; the other entry functions keep the server's
  `Box<dyn Error>`, which `ox` converts with `anyhow!("{error}")` because the
  box is not `Send + Sync`.
- `skills_directories_load_in_priority_order` reorders its expected list:
  `ox-only` sorts before `personal`, where `ur-only` sorted after `shared`.

## Automated checks

- `make check` — Passed.
- `make e2e` — Passed.

## Manual verification

1. A headless run through `ox run`.

   ```sh
   python3 scripts/run.py 'Reply with the single word pong.'
   ```

   Printed `pong`.

2. The Ox client starts the Ox server through `ox acp` and answers a prompt,
   with an empty home directory and data directory.

   ```sh
   T=$(mktemp -d); mkdir $T/ws
   env HOME=$T OX_DATA_DIR=$T/data OPENROUTER_API_KEY=... target/debug/ox --dir $T/ws
   ```

   `ps` showed `target/debug/ox acp` as the child process, the prompt was
   answered with `pong`, and `$T/data/ox.db` was created.

## Follow-up work

- `ox acp auth login` from Zed's terminal authentication is untested, as the
  existing TODO on `terminal_auth_method` notes.
