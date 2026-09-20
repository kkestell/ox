#!/usr/bin/env bash

set -uo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly PROMPT_FILE="$ROOT/eng/prompts/acp-agent-research.md"
readonly REPORT_DIR="$ROOT/eng/research"
readonly TMP_PARENT="$ROOT/tmp"
readonly TMP_ROOT="$TMP_PARENT/acp-agent-research"
readonly MODEL="openrouter/z-ai/glm-5.3-flash"
readonly TIME_LIMIT="20m"
readonly MIN_REPORT_BYTES=1024
readonly MAX_REPOSITORIES=9
readonly CONCURRENT_REPOSITORIES=2

# A mix of mature native ACP agents and focused adapters. This should expose
# meaningful differences in session ownership, durability, and process models.
readonly REPOSITORIES=(
  "google-gemini/gemini-cli"
  "OpenHands/OpenHands"
  "xai-org/grok-build"
  "AntigmaLabs/ante"
  "stakpak/agent"
  "vinhnx/VTCode"
  "Muvon/octomind"
)

if [[ ! -f "$PROMPT_FILE" ]]; then
  printf 'ERROR: research prompt not found: %s\n' "$PROMPT_FILE" >&2
  exit 1
fi

if ! command -v git >/dev/null 2>&1; then
  printf 'ERROR: git is not installed or is not on PATH\n' >&2
  exit 1
fi

if ! command -v opencode >/dev/null 2>&1; then
  printf 'ERROR: opencode is not installed or is not on PATH\n' >&2
  exit 1
fi

if ! opencode models openrouter | grep -Fqx "$MODEL"; then
  printf 'ERROR: OpenCode model is not available: %s\n' "$MODEL" >&2
  exit 1
fi

if command -v timeout >/dev/null 2>&1; then
  readonly TIMEOUT_BIN="$(command -v timeout)"
elif command -v gtimeout >/dev/null 2>&1; then
  readonly TIMEOUT_BIN="$(command -v gtimeout)"
else
  printf 'ERROR: GNU timeout is required (install coreutils on macOS)\n' >&2
  exit 1
fi

cleanup() {
  rm -rf "$TMP_ROOT"
  rmdir "$TMP_PARENT" 2>/dev/null || true
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

rm -rf "$TMP_ROOT"
readonly STATUS_DIR="$TMP_ROOT/status"
mkdir -p "$STATUS_DIR" "$REPORT_DIR"

run_repository() {
  local repository="$1"
  local slug repository_url checkout report_relative report_path revision prompt
  local run_status report_size

  slug="${repository/\//--}"
  repository_url="https://github.com/$repository"
  checkout="$TMP_ROOT/$slug"
  report_relative="eng/research/$slug.md"
  report_path="$ROOT/$report_relative"

  printf '\n[%s] Cloning %s\n' "$repository" "$repository_url"
  if ! GIT_TERMINAL_PROMPT=0 git clone \
    --depth 1 \
    --single-branch \
    --no-tags \
    "$repository_url.git" \
    "$checkout"; then
    printf '[ERROR] %s: clone failed\n' "$repository" >&2
    rm -rf "$checkout"
    return 1
  fi

  revision="$(git -C "$checkout" rev-parse HEAD)"
  prompt="$(sed \
    -e "s|<owner/repository>|$repository|g" \
    -e "s|<https://github.com/owner/repository>|$repository_url|g" \
    -e "s|<full commit SHA>|$revision|g" \
    -e "s|<absolute path to the checkout>|$checkout|g" \
    -e "s|eng/research/<owner>--<repository>\.md|$report_relative|g" \
    "$PROMPT_FILE")"

  # Prevent a stale or partial report from making a failed rerun look healthy.
  rm -f "$report_path"

  printf '[%s] Running OpenCode at %s\n' "$repository" "$revision"
  "$TIMEOUT_BIN" \
    --signal=TERM \
    --kill-after=15s \
    "$TIME_LIMIT" \
    opencode run \
      --auto \
      --dir "$ROOT" \
      --model "$MODEL" \
      --title "Research $repository ACP architecture" \
      "$prompt"
  run_status=$?

  if [[ "$run_status" -eq 124 ]]; then
    printf '[ERROR] %s: OpenCode exceeded %s and was terminated\n' \
      "$repository" "$TIME_LIMIT" >&2
    rm -rf "$checkout"
    return 1
  elif [[ "$run_status" -ne 0 ]]; then
    printf '[ERROR] %s: OpenCode exited with status %d\n' \
      "$repository" "$run_status" >&2
    rm -rf "$checkout"
    return 1
  elif [[ ! -f "$report_path" ]]; then
    printf '[ERROR] %s: report was not created: %s\n' \
      "$repository" "$report_relative" >&2
    rm -rf "$checkout"
    return 1
  else
    report_size="$(wc -c < "$report_path" | tr -d '[:space:]')"
    if [[ "$report_size" -lt "$MIN_REPORT_BYTES" ]]; then
      printf '[ERROR] %s: report is only %s bytes: %s\n' \
        "$repository" "$report_size" "$report_relative" >&2
      rm -rf "$checkout"
      return 1
    else
      printf '[SUCCESS] %s: %s bytes written to %s\n' \
        "$repository" "$report_size" "$report_relative"
    fi
  fi

  rm -rf "$checkout"
  return 0
}

repository_count="${#REPOSITORIES[@]}"
if [[ "$repository_count" -gt "$MAX_REPOSITORIES" ]]; then
  repository_count="$MAX_REPOSITORIES"
fi

run_worker() {
  local worker="$1"
  local index repository slug

  for ((index = worker; index < repository_count; index += CONCURRENT_REPOSITORIES)); do
    repository="${REPOSITORIES[$index]}"
    slug="${repository/\//--}"
    if run_repository "$repository"; then
      : > "$STATUS_DIR/$slug.success"
    else
      : > "$STATUS_DIR/$slug.failure"
    fi
  done
}

worker_pids=()
for ((worker = 0; worker < CONCURRENT_REPOSITORIES && worker < repository_count; worker++)); do
  (trap - EXIT INT TERM; run_worker "$worker") &
  worker_pids+=("$!")
done

for worker_pid in "${worker_pids[@]}"; do
  wait "$worker_pid" || true
done

successes="$(find "$STATUS_DIR" -type f -name '*.success' | wc -l | tr -d '[:space:]')"
failures="$(find "$STATUS_DIR" -type f -name '*.failure' | wc -l | tr -d '[:space:]')"
completed=$((successes + failures))
if [[ "$completed" -lt "$repository_count" ]]; then
  failures=$((failures + repository_count - completed))
fi

printf '\nCompleted: %d succeeded, %d failed\n' "$successes" "$failures"

if [[ "$failures" -ne 0 ]]; then
  exit 1
fi
