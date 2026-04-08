#!/usr/bin/env bash
# Converts ReSharper InspectCode SARIF output into a concise, one-line-per-issue
# format suitable for quick scanning or feeding into editors/CI.
#
# Usage: ./scripts/format-inspection-results.sh [input.xml]
# Defaults to inspection-results.xml in the repo root.

set -euo pipefail

input="${1:-inspection-results.xml}"

if [[ ! -f "$input" ]]; then
  echo "error: $input not found. Run 'make inspect' first." >&2
  exit 1
fi

jq -r '
  .runs[].results[] |
  .level as $level |
  .message.text as $msg |
  .locations[].physicalLocation |
  "\(.artifactLocation.uri):\(.region.startLine): [\($level)] \($msg)"
' "$input" | sort
