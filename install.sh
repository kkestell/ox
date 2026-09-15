#!/bin/sh
# Installs the ox executable from this archive and, when the global settings
# file does not exist yet, writes a starter one. Re-running it upgrades the
# executable and leaves existing settings alone.

set -eu

archive="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
binary="$archive/ox"
bindir="${HOME:?HOME must be set}/.local/bin"
# Ox reads XDG_CONFIG_HOME only when it is absolute, so a relative one must not
# send this file somewhere Ox will never look.
config="$HOME/.config/ox"
case "${XDG_CONFIG_HOME:-}" in
    /*) config="$XDG_CONFIG_HOME/ox" ;;
esac
settings="$config/settings.json"

if [ ! -f "$binary" ]; then
    echo "install.sh: no ox executable beside this script in $archive" >&2
    exit 1
fi

mkdir -p "$bindir"

# Replacing through a temporary file keeps the install atomic and avoids
# "text file busy" when an ACP client is still running the old executable.
staged="$bindir/.ox.$$"
trap 'rm -f "$staged"' EXIT INT TERM
cp "$binary" "$staged"
chmod 0755 "$staged"
if command -v xattr > /dev/null 2>&1; then
    # A browser download marks the archive as quarantined, which Gatekeeper
    # refuses to run.
    xattr -c "$staged" 2> /dev/null || true
fi
mv -f "$staged" "$bindir/ox"
echo "installed $bindir/ox"

if [ -e "$settings" ]; then
    echo "kept $settings"
else
    mkdir -p "$config"
    cat > "$settings" <<'JSON'
{
  "default_model": "deepseek/deepseek-v4.1-flash",
  "models": {
    "openai/gpt-5.6-luna": {
      "provider": { "only": ["openai"] }
    },
    "z-ai/glm-5.3-flash": {
      "provider": { "only": ["z-ai"] }
    },
    "deepseek/deepseek-v4.1-flash": {
      "provider": { "only": ["deepseek"] }
    },
    "google/gemini-3.8-flash": {
      "provider": { "only": ["google-ai-studio"] }
    }
  }
}
JSON
    echo "wrote $settings"
fi

case ":$PATH:" in
    *":$bindir:"*) ;;
    *) echo "add $bindir to your PATH" ;;
esac

echo "next: ox login, then edit $settings"
