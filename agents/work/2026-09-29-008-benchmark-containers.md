# Benchmark containers

## Plan

`agents/plans/2026-09-29-008-benchmark-containers.md`

## Summary

`scripts/bench.py run` builds Ox as a Linux binary in a build container and runs
each repetition in its own benchmark container with no host directory mounted.
Concurrent benchmark runs build, run, and clean up independently. The plan's
goal is met.

## Departures from the plan

- The build container does not use a per-label source path. Cargo identifies
  Ox's crates by their path relative to the workspace, so `/src/<label>` did not
  separate their fingerprints. After building `cb04b53` from one path, building
  the checkout from another path finished in 0.11 s and produced the `cb04b53`
  binary. Every build now mounts its source at `/src`, runs under
  `flock /target/bench.lock`, and runs
  `cargo clean --profile fast -p ox -p ox-server` before `cargo build`.
  Dependencies stay shared, and Ox's two crates rebuild in about 8 seconds. The
  user chose this fix.
- The script sets the built binary to mode 0755 before renaming it. Docker
  Desktop's file sharing created it as 0600, which the benchmark container could
  not execute.

## Automated checks

- `make check` — passed.
- `make check-docs` — passed.

## Manual verification

1. Two concurrent smoke runs, one building the checkout and one building
   `--ref HEAD`.

   ```sh
   scripts/bench.py run --label smoke --reps 1 --task pi-digit tinyexpr-precedence \
     --model deepseek/deepseek-v4.1-flash --effort default
   # in a second terminal, while the first runs:
   scripts/bench.py run --label smoke-ref --ref HEAD --reps 1 \
     --task pi-digit tinyexpr-precedence \
     --model deepseek/deepseek-v4.1-flash --effort default
   scripts/bench.py compare smoke smoke-ref
   docker ps -a --filter label=ox-bench
   ```

   All four repetitions finished and passed. `compare` showed both labels, each
   run directory had `workspace/` and `data/ox.db`, `docker ps` listed no
   containers, and `bench/worktree/` was empty.

2. Concurrent builds of different commits produce each commit's binary. Build
   `cb04b53` from a worktree and the checkout at the same time, using the
   `docker run` command from `cargo_build`, and compare the binaries' SHA-256
   hashes.

   Each binary matched its own commit: `365b6f…` for `cb04b53` and `2668a9…` for
   the checkout, the same hash as `bench/bin/ox-smoke`. A second concurrent pair
   of runs through the script (`--label final` and
   `--label final-ref --ref
   HEAD`, `--task pi-digit`) passed.

3. Ctrl-C during a repetition. Start `scripts/bench.py run --label interrupt`
   with two tasks and send SIGINT to its process group while both benchmark
   containers run.

   The script exited with status 130 within 0.3 seconds, and
   `docker ps -a --filter label=ox-bench` listed no containers.

## Follow-up work

- A check command still has no timeout.
