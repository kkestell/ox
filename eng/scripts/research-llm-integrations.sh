#!/usr/bin/env bash

set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly PROMPT_FILE="$ROOT/eng/prompts/llm-integration-research.md"
readonly TMP_ROOT="$ROOT/tmp/llm-integration-research"
readonly MODEL="openrouter/deepseek/deepseek-v4-flash-0731"
readonly TIME_LIMIT="20m"
readonly CONCURRENT_REPOSITORIES=4

# Follow-up set: the reports omitted from the initial run. These revisions
# match their existing individual research reports.
readonly PROJECTS=(
  "AntigmaLabs/ante 9eca8821e004cd76a1c75e396fb9a78c7e06b67a"
  "Muvon/octomind 4bffaab3d37a1259ddb9fb2170b8067efcf72cb3"
  "stakpak/agent 760cd2b5984d29c2d513bb15ca33e995fae45f17"
  "vinhnx/VTCode bf0db9fda60c6f3fe97050245212f40fae425ea7"
)

for requirement in git opencode; do
  command -v "$requirement" >/dev/null || {
    printf 'ERROR: %s is required\n' "$requirement" >&2
    exit 1
  }
done

if command -v timeout >/dev/null; then
  readonly TIMEOUT_BIN="$(command -v timeout)"
elif command -v gtimeout >/dev/null; then
  readonly TIMEOUT_BIN="$(command -v gtimeout)"
else
  printf 'ERROR: GNU timeout is required (install coreutils on macOS)\n' >&2
  exit 1
fi

if [[ ! -f "$PROMPT_FILE" ]]; then
  printf 'ERROR: prompt not found: %s\n' "$PROMPT_FILE" >&2
  exit 1
fi

if ! opencode models openrouter | grep -Fqx "$MODEL"; then
  printf 'ERROR: unavailable OpenCode model: %s\n' "$MODEL" >&2
  exit 1
fi

mkdir -p "$TMP_ROOT"

cleanup() {
  rm -rf "$TMP_ROOT"
  rmdir "$(dirname "$TMP_ROOT")" 2>/dev/null || true
}
trap cleanup EXIT

run_project() {
  local project="$1" revision="$2" slug checkout report_relative prompt
  slug="${project//\//--}"
  checkout="$TMP_ROOT/$slug"
  report_relative="eng/research/$slug.md"

  if [[ ! -f "$ROOT/$report_relative" ]]; then
    printf 'ERROR: report not found: %s\n' "$report_relative" >&2
    return 1
  fi

  git init --quiet "$checkout"
  git -C "$checkout" remote add origin "https://github.com/$project.git"
  git -C "$checkout" fetch --quiet --depth 1 origin "$revision"
  git -C "$checkout" checkout --quiet --detach FETCH_HEAD

  prompt="$(sed \
    -e "s|<owner/repository>|$project|g" \
    -e "s|<https://github.com/owner/repository>|https://github.com/$project|g" \
    -e "s|<full commit SHA>|$revision|g" \
    -e "s|<absolute path to the checkout>|$checkout|g" \
    -e "s|eng/research/<owner>--<repository>\\.md|$report_relative|g" \
    "$PROMPT_FILE")"

  printf '[%s] researching %s\n' "$project" "$revision"
  "$TIMEOUT_BIN" --signal=TERM --kill-after=15s "$TIME_LIMIT" \
    opencode run --auto --dir "$ROOT" --model "$MODEL" \
      --title "Research $project LLM integration" "$prompt"
}

run_worker() {
  local worker="$1" index project revision
  for ((index = worker; index < ${#PROJECTS[@]}; index += CONCURRENT_REPOSITORIES)); do
    read -r project revision <<< "${PROJECTS[$index]}"
    run_project "$project" "$revision"
  done
}

worker_pids=()
for ((worker = 0; worker < CONCURRENT_REPOSITORIES; worker++)); do
  run_worker "$worker" &
  worker_pids+=("$!")
done
for worker_pid in "${worker_pids[@]}"; do
  wait "$worker_pid"
done

printf 'Updated %d individual reports.\n' "${#PROJECTS[@]}"
