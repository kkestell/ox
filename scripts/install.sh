#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
INSTALL_DIR="$HOME/.local/bin"
UR_CONFIG_DIR="$HOME/.ur"

# Publish the Ur.Tui project as a native AoT binary.
dotnet publish "$REPO_ROOT/src/Ur.Tui/Ur.Tui.csproj" \
    --configuration Release \
    --output "$REPO_ROOT/publish"

# Install the binary to ~/.local/bin/ur.
# Remove existing binary first to avoid "Text file busy" error when overwriting a running executable.
mkdir -p "$INSTALL_DIR"
rm -f "$INSTALL_DIR/ur"
cp "$REPO_ROOT/publish/Ur.Tui" "$INSTALL_DIR/ur"
chmod +x "$INSTALL_DIR/ur"

# Create the config directory if it doesn't exist yet.
mkdir -p "$UR_CONFIG_DIR"

echo "Installed ur to $INSTALL_DIR/ur"
