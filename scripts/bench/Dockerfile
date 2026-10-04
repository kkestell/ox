# The benchmark image: runs the benchmark tasks, some of which build Rust code.
FROM rust:1-bookworm
RUN apt-get update \
    && apt-get install -y --no-install-recommends procps python3 ripgrep \
    && rm -rf /var/lib/apt/lists/* \
    && rustup component add clippy
