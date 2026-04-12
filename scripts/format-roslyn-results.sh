#!/usr/bin/env bash
# Converts dotnet build Roslyn analyzer output into the same one-line-per-issue
# format used by format-inspection-results.sh: file:line: [level] message
#
# Usage: dotnet build ... 2>&1 | ./scripts/format-roslyn-results.sh

set -uo pipefail

# Dotnet build format: /path/to/File.cs(line,col): warning CAXXXX: message [project.csproj]
# Target format:       /path/to/File.cs:line: [warning] CAXXXX: message
grep -E '\([0-9]+,[0-9]+\): (warning|error) ' \
  | sed -E 's|^(.*)\(([0-9]+),[0-9]+\): (warning|error) (.+) \[.*\]$|\1:\2: [\3] \4|' \
  | sort \
  || true
