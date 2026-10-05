# The benchmark image: builds and tests small-c, which needs Go, QBE, and a C
# compiler as cc.
FROM golang:1.27-bookworm
RUN apt-get update \
    && apt-get install -y --no-install-recommends procps python3 ripgrep xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL https://c9x.me/compile/release/qbe-1.2.tar.xz | tar -xJ -C /tmp \
    && make -C /tmp/qbe-1.2 install \
    && rm -rf /tmp/qbe-1.2
