#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: find_intent_docs.sh <target-path>

Resolve the primary verification document plus nearby plan or brainstorm docs.

Prints:
repo_root=...
primary_path=...
plan_path=...
brainstorm_path=...
candidate=...
EOF
}

if (($# != 1)); then
  usage >&2
  exit 1
fi

target_path="$1"

repo_root=$(git rev-parse --show-toplevel)
declare -A seen=()
declare -a candidates=()

resolve_path() {
  local path="$1"

  if [[ "$path" = /* ]]; then
    printf '%s\n' "$path"
    return 0
  fi

  if [[ -e "$path" ]]; then
    (
      cd "$(dirname "$path")"
      printf '%s/%s\n' "$(pwd -P)" "$(basename "$path")"
    )
    return 0
  fi

  printf '%s/%s\n' "$repo_root" "$path"
}

add_candidate() {
  local candidate="$1"
  [[ -n "$candidate" ]] || return 0
  candidate="$(resolve_path "$candidate")"
  if [[ -f "$candidate" && -z "${seen[$candidate]:-}" ]]; then
    seen["$candidate"]=1
    candidates+=("$candidate")
  fi
}

find_linked_docs() {
  local source_path="$1"
  local source_dir
  source_dir="$(dirname "$source_path")"
  local ref

  while IFS= read -r ref; do
    [[ -n "$ref" ]] || continue
    if [[ "$ref" = /* ]]; then
      add_candidate "$ref"
    else
      add_candidate "${source_dir}/${ref}"
      add_candidate "${repo_root}/${ref}"
    fi
  done < <(rg -o --no-filename '(?:/)?(?:[A-Za-z0-9._-]+/)*(?:plan|brainstorm)\.md|(?:/)?(?:[A-Za-z0-9._-]+/)*docs/plans/[A-Za-z0-9._-]+\.md' "$source_path" || true)
}

primary_path=""
resolved_target="$(resolve_path "$target_path")"

if [[ -f "$resolved_target" ]]; then
  add_candidate "$resolved_target"
  primary_path="$resolved_target"
  find_linked_docs "$resolved_target"

  case "$resolved_target" in
    */plan.md|*/docs/plans/*.md)
      add_candidate "${resolved_target%/*}/brainstorm.md"
      ;;
    */brainstorm.md)
      add_candidate "${resolved_target%/*}/plan.md"
      ;;
  esac
elif [[ -d "$resolved_target" ]]; then
  add_candidate "${resolved_target}/plan.md"
  add_candidate "${resolved_target}/brainstorm.md"

  if [[ -z "$primary_path" && -f "${resolved_target}/plan.md" ]]; then
    primary_path="$(resolve_path "${resolved_target}/plan.md")"
  elif [[ -z "$primary_path" && -f "${resolved_target}/brainstorm.md" ]]; then
    primary_path="$(resolve_path "${resolved_target}/brainstorm.md")"
  fi
fi

plan_path=""
brainstorm_path=""

for candidate in "${candidates[@]}"; do
  case "$candidate" in
    */plan.md|*/docs/plans/*.md)
      if [[ -z "$plan_path" ]]; then
        plan_path="$candidate"
      fi
      ;;
    */brainstorm.md)
      if [[ -z "$brainstorm_path" ]]; then
        brainstorm_path="$candidate"
      fi
      ;;
  esac
done

if [[ -z "$primary_path" ]]; then
  if [[ -n "$plan_path" ]]; then
    primary_path="$plan_path"
  elif [[ -n "$brainstorm_path" ]]; then
    primary_path="$brainstorm_path"
  fi
fi

printf 'repo_root=%s\n' "$repo_root"
printf 'primary_path=%s\n' "$primary_path"
printf 'plan_path=%s\n' "$plan_path"
printf 'brainstorm_path=%s\n' "$brainstorm_path"
for candidate in "${candidates[@]}"; do
  printf 'candidate=%s\n' "$candidate"
done
