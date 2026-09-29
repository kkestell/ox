# Rename agent message to subagent message

## Plan

`agents/plans/2026-09-29-003-subagent-message-rename.md`

## Summary

Every subagent message name in `ox-acp` now says "subagent message": the types,
the transcript entry, the functions, the saved entry kind, the error text, the
tool call ID prefix, and the glossary entry. ACP's `AgentMessageChunk` and
`agent_message_chunk` are unchanged. OX-0007 is marked fixed. The plan's goal is
met.

## Decisions

- The rename ran as a scripted replace on the plan's names with word boundaries,
  so `AgentMessageChunk` could not match. The diff has no line that mentions a
  chunk.
- Line counts from `rsloc crates` against HEAD: production 11439 to 11443 (+4),
  tests 12309 to 12316 (+7), docs unchanged. The growth is `rustfmt` wrapping
  lines that the longer names pushed past the width limit.

## Automated checks

- `make check` — Passed: dprint, rustfmt, all workspace tests (164 `ox-acp`
  tests), build, and clippy.
- `make e2e` — Not run. No terminal behavior changed, and the client crate has
  no diff.

## Manual verification

1. No old name remains outside ACP's chunk names.

   ```sh
   grep -rnE 'AgentMessage|agent_message|agent-message|[Aa]gent messages?' crates \
     | grep -vE 'AgentMessageChunk|agent_message_chunk|[Ss]ubagent'
   ```

   One line: the fake server's "agent message chunk" comment, which the plan
   keeps.
