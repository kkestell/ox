# Benchmark containers

## Goal

A benchmark repetition runs Ox's shell with the user's permissions on the host,
so a model mistake can reach anything the user can, and a repetition's builds
can fill the host disk. Each repetition runs in its own Docker container
instead, with no host directory mounted. Several `bench.py run` invocations can
run at once without interfering with each other. `compare`, the metrics, and the
task set do not change.

## Related code

- `scripts/bench.py` `build` and `cargo_build` — build a commit in
  `bench/worktree/<commit>` or build the checkout, with the host target
  directory `target/bench`, and reuse `bench/bin/ox-<commit>`.
- `scripts/bench.py` `run_repetition` — creates the workspace on the host,
  clones the task's repository, runs Ox with `Popen`, applies the timeout with
  `SIGTERM` and `SIGKILL`, runs the check command, and reads `data/ox.db`.
- `scripts/bench.py` `run` — the thread pool of repetitions.
- `scripts/bench.py` `transcript_metrics` — reads `data/ox.db`; unchanged.
- `crates/ox-server/src/auth.rs:15` `api_key` — `OPENROUTER_API_KEY` wins over
  the keyring, so a Linux container never reaches a keyring.
- `.github/workflows/release.yml` — Ox already builds on stock Ubuntu with no
  extra system packages; `reqwest` uses rustls.

## Decisions

- **One image builds Ox and runs the tasks.** `scripts/bench/Dockerfile` starts
  from `rust:1-bookworm`, which has `cargo`, `gcc`, `make`, `git`, `curl`, and
  CA certificates, and adds `python3`. The tasks need exactly these. Every
  `bench.py run` runs `docker build -t ox-bench scripts/bench` first; the layer
  cache makes that instant after the first time.
- **Ox is built in a container, as a Linux binary.** A macOS binary cannot run
  in the container, and building in the same image avoids a cross-compiler. The
  build container mounts the source read-only at `/src/<label>`, keeps its
  target directory in the Docker volume `ox-bench-target` and its Cargo registry
  in `ox-bench-cargo`, and writes the binary through a mounted `bench/bin/`. The
  volumes keep Linux build output off the host disk and let every build share
  dependencies. Cargo's lock on the target directory serializes concurrent
  builds.
- **Each label builds from its own source path.** Cargo decides whether a path
  crate is fresh by comparing file times, not content. Two checkouts built from
  the same path could reuse each other's output when the second checkout's files
  are older than the first build. A distinct `/src/<label>` gives each label its
  own fingerprints, at the cost of rebuilding Ox's own crates once per label.
  For the same reason the worktree for `--ref` moves to
  `bench/worktree/<label>`, so two invocations building the same commit do not
  collide.
- **Binaries are written under a temporary name and renamed.** A concurrent
  invocation that finds `bench/bin/ox-<commit>` then never reads a partial file.
  The build writes `ox-<name>.tmp-<label>` and the script renames it with
  `os.replace`.
- **A repetition's files stay in its container until the check finishes.** The
  benchmark container runs `sleep infinity` and gets the binary with
  `docker cp`. `docker exec` then clones the repository into `/workspace` (or
  creates it), runs Ox, and runs the check command. Afterward the script copies
  `/workspace` and `/data` to the run directory with `docker cp` and removes the
  container. Nothing on the host is writable from inside a benchmark container.
- **The API key reaches only the Ox command.** It is passed with
  `docker exec -e OPENROUTER_API_KEY`, taking the value from the `docker`
  process's environment so it never appears on a command line. The check command
  runs without it.
- **The timeout runs inside the container.** Ox runs under
  `timeout --signal=TERM --kill-after=30 <seconds>`. Exit status 124 or 137 is
  status `timeout`. Signals sent to a `docker exec` client do not reach the
  process in the container, so the host-side `SIGTERM` and `SIGKILL` go away.
- **Ctrl-C removes the benchmark run's containers.** Every benchmark container
  carries the Docker label `ox-bench=<label>`. On `KeyboardInterrupt`, `run`
  cancels the queued repetitions and runs `docker rm -f` on the containers with
  that label, which ends the running `docker exec` calls, then exits. Without
  this, `sleep infinity` containers outlive the script.
- **Concurrent invocations need nothing else.** Labels are unique, and container
  names are `ox-bench-<label>-<task id>-<rep>`, so repetitions of different
  benchmark runs never share a name, directory, or database.

## Naming

- `benchmark image` — the Docker image `ox-bench`, built from
  `scripts/bench/Dockerfile`.
- `build container` — the container that builds Ox for one benchmark run.
- `benchmark container` — the container one repetition runs in.
- `benchmark run`, `label`, `task`, `check command`, `repetition`, `result`,
  `passed`, and `status` as defined in
  `agents/plans/2026-09-29-007-benchmark-script.md`.

## Test plan

- The script has no automated tests. After the change, run
  `scripts/bench.py run --label smoke --reps 1 --task pi-digit tinyexpr-precedence
  --model deepseek/deepseek-v4.1-flash --effort default`
  and, while it runs, the same command with `--label smoke-ref --ref HEAD` in a
  second terminal. Both pass; `compare smoke smoke-ref` shows both;
  `docker ps -a --filter
  label=ox-bench` is empty afterward; each run
  directory has `workspace/` and `data/ox.db`.
- Interrupt a third run with Ctrl-C during a repetition and confirm its
  containers are gone.

## Implementation plan

1. `scripts/bench/Dockerfile`: `FROM rust:1-bookworm`, install `python3`.
2. `scripts/bench.py` `build`: build the benchmark image, then build Ox in a
   build container as decided. `--ref` keeps its worktree, now at
   `bench/worktree/<label>`, and still removes it afterward; the checkout build
   mounts the repository root. Drop the host `target/bench`.
3. `scripts/bench.py` `run_repetition`: start the benchmark container, copy in
   the binary, set up `/workspace`, run Ox under `timeout` with `OX_DATA_DIR`
   set to `/data`, run the check command with `docker exec -w /workspace`, copy
   `/workspace` and `/data` out, and remove the container in a `finally`. Time
   only the Ox command.
4. `scripts/bench.py` `run`: remove the benchmark run's containers on
   `KeyboardInterrupt`.
5. Delete `bench/bin/`, whose binaries are macOS builds, `bench/runs/`, and
   `target/bench/`. Run the checks in the test plan.

## Documentation updates

- `AGENTS.md`: the `scripts/bench/tasks.toml` entry becomes `scripts/bench/` —
  the benchmark tasks and the benchmark image.
- `agents/testing.md`: the benchmark line says the tasks run in Docker
  containers.
