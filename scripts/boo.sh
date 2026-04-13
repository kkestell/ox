#!/usr/bin/env bash
# Builds Ox and Boo, sets up an isolated temp workspace with a sample
# skill, then launches a headless Boo session pointing at Ox.
#
# By default runs with the fake provider for deterministic, credential-free
# testing. Pass --live to use a real provider (requires .env with API key).
#
# Tear down with: cd boo && uv run boo stop && rm -rf /tmp/ox-test

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORKSPACE="/tmp/ox-test"
OX="$REPO_ROOT/src/Ox/bin/Debug/net10.0/Ox"

# --- Parse arguments ---------------------------------------------------------

SCENARIO="hello"
LIVE_MODE=false

for arg in "$@"; do
    case "$arg" in
        --live)
            LIVE_MODE=true
            ;;
        --scenario=*)
            SCENARIO="${arg#--scenario=}"
            ;;
    esac
done

# --- Build -------------------------------------------------------------------

echo "==> Building Ox..."
dotnet build "$REPO_ROOT/src/Ox/Ox.csproj" --nologo -v quiet

echo "==> Building Boo..."
make -C "$REPO_ROOT/boo" build

# --- Workspace ---------------------------------------------------------------

echo "==> Setting up workspace at $WORKSPACE..."
rm -rf "$WORKSPACE"
mkdir -p "$WORKSPACE"

if [ "$LIVE_MODE" = true ]; then
    # Live mode: copy .env so the API key is available inside the sandbox.
    ENV_DIR="$REPO_ROOT"
    while [ ! -f "$ENV_DIR/.env" ] && [ "$ENV_DIR" != "/" ]; do
        ENV_DIR="$(dirname "$ENV_DIR")"
    done
    if [ -f "$ENV_DIR/.env" ]; then
        cp "$ENV_DIR/.env" "$WORKSPACE/.env"
    else
        echo "Warning: no .env found — API key may be missing" >&2
    fi
fi

# A plain file for read_file tests.
echo "test-sentinel" > "$WORKSPACE/hello.txt"

# Sample skill: /greet <name>
mkdir -p "$WORKSPACE/.ox/skills/greet"
cat > "$WORKSPACE/.ox/skills/greet/SKILL.md" << 'SKILL_EOF'
---
name: greet
description: Greet the user warmly
user-invocable: true
arguments: name
---

Greet the user by name. Their name is: $ARGUMENTS

Say "Hello, <name>! Boo says hi." and nothing else.
SKILL_EOF

# --- Launch ------------------------------------------------------------------

# Build the Ox command. In fake mode (default), pass --fake-provider so the
# session is deterministic and doesn't need credentials.
OX_CMD="cd $WORKSPACE && $OX"
if [ "$LIVE_MODE" = false ]; then
    OX_CMD="cd $WORKSPACE && $OX --fake-provider $SCENARIO"
fi

echo "==> Launching Boo session (${LIVE_MODE:+live}${LIVE_MODE:-fake:$SCENARIO})..."
cd "$REPO_ROOT/boo"
uv run boo start "$OX_CMD" \
    --cols 120 --rows 50

echo ""
echo "Session is up. Quick-start:"
echo "  cd $REPO_ROOT/boo"
echo "  uv run boo screen          # see what's on screen"
echo "  uv run boo type 'hello\n'  # send input"
echo "  uv run boo stop            # tear down"
