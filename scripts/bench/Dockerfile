# The benchmark image: builds Ox and runs the benchmark tasks.
FROM rust:1-bookworm
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*
