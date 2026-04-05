#!/usr/bin/env bash
# Builds Ur.Tui and Boo, sets up an isolated temp workspace with a sample
# skill and extension, then launches a visible Boo session pointing at
# Ur.Tui. Tear down with: cd boo && uv run boo stop && rm -rf /tmp/ur-tui-test
#
# Usage: ./scripts/boo.sh [--headless]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORKSPACE="/tmp/ur-tui-test"
UR_TUI="$REPO_ROOT/src/Ur.Tui/bin/Debug/net10.0/Ur.Tui"

visible="--visible"
if [[ "${1:-}" == "--headless" ]]; then
  visible=""
fi

# --- Build -------------------------------------------------------------------

echo "==> Building Ur.Tui..."
dotnet build "$REPO_ROOT/src/Ur.Tui/Ur.Tui.csproj" --nologo -v quiet

echo "==> Building Boo..."
make -C "$REPO_ROOT/boo" build

# --- Workspace ---------------------------------------------------------------

echo "==> Setting up workspace at $WORKSPACE..."
rm -rf "$WORKSPACE"
mkdir -p "$WORKSPACE"

# Copy the .env so the API key is available inside the sandbox.
cp "$REPO_ROOT/.env" "$WORKSPACE/.env"

# A plain file for read_file tests.
echo "test-sentinel" > "$WORKSPACE/hello.txt"

# Sample skill: /greet <name>
mkdir -p "$WORKSPACE/.ur/skills/greet"
cat > "$WORKSPACE/.ur/skills/greet/SKILL.md" << 'SKILL_EOF'
---
name: greet
description: Greet the user warmly
user-invocable: true
arguments: name
---

Greet the user by name. Their name is: $ARGUMENTS

Say "Hello, <name>! Boo says hi." and nothing else.
SKILL_EOF

# Sample extension: roll_dice
mkdir -p "$WORKSPACE/.ur/extensions/dice"

cat > "$WORKSPACE/.ur/extensions/dice/manifest.lua" << 'MANIFEST_EOF'
return {
  name = "dice",
  version = "1.0.0",
  description = "A dice-rolling extension for testing."
}
MANIFEST_EOF

cat > "$WORKSPACE/.ur/extensions/dice/main.lua" << 'MAIN_EOF'
ur.tool.register({
  name = "roll_dice",
  description = "Roll a six-sided die and return the result.",
  parameters = {
    type = "object",
    properties = {}
  },
  handler = function(args)
    return "You rolled a 6!"
  end
})
MAIN_EOF

# Enable the workspace extension via the per-workspace state file.
HASH=$(printf '%s' "$WORKSPACE" | sha256sum | cut -d' ' -f1)
mkdir -p "$HOME/.ur/workspaces/$HASH"

cat > "$HOME/.ur/workspaces/$HASH/extensions-state.json" << EXT_EOF
{
  "version": 1,
  "workspacePath": "$WORKSPACE",
  "extensions": {
    "workspace:dice": true
  }
}
EXT_EOF

# --- Launch ------------------------------------------------------------------

echo "==> Launching Boo session..."
cd "$REPO_ROOT/boo"
uv run boo start "cd $WORKSPACE && $UR_TUI" \
    --cols 120 --rows 50 $visible

echo ""
echo "Session is up. Quick-start:"
echo "  cd $REPO_ROOT/boo"
echo "  uv run boo screen          # see what's on screen"
echo "  uv run boo type 'hello\n'  # send input"
echo "  uv run boo stop            # tear down"
