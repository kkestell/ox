# The evaluation image: Rust, a C compiler, make, and Git for the Oberon
# compiler tasks, Python for the agent's scripts, bash for the shell tool, and
# ripgrep for the search tools.
FROM rust:1-slim-bookworm
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates git make procps python3 python-is-python3 ripgrep \
    && rm -rf /var/lib/apt/lists/*
