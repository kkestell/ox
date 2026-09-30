# The benchmark image: builds Ox and runs the benchmark tasks.
FROM rust:1-bookworm
RUN apt-get update \
    && apt-get install -y --no-install-recommends procps python3 ripgrep \
    && rm -rf /var/lib/apt/lists/*
