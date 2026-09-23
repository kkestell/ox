# TODO

- [x] src/tools/patch.rs:33 parse has a cognitive score of 51 across 101 lines. Break it up into a small state machine or one function per kind of patch section. Planned in eng/plans/2026-09-23-005-patch-parse-and-model-loop-refactor.md.
- [x] src/acp/prompt.rs:325 run_model_loop has a cognitive score of 34 in 64 lines. It is the core agent loop; split it so each fallible step reads directly. Planned in eng/plans/2026-09-23-005-patch-parse-and-model-loop-refactor.md.
- [x] src/tools/search.rs:84 run has a cognitive score of 36 across 85 lines. Split it into smaller steps. Planned in eng/plans/2026-09-23-006-search-run-and-transcript-validation-refactor.md.
- [x] src/sessions.rs:473 validate_transcript has a cognitive score of 33 across 132 lines. Split it into one check per validation rule. Planned in eng/plans/2026-09-23-006-search-run-and-transcript-validation-refactor.md.
- [x] src/acp.rs:642 serve is 183 lines, the longest function in the codebase (cognitive score 20). Move nontrivial ACP request handler bodies into ServerState methods while keeping connection setup and shutdown in serve. Planned in eng/plans/2026-09-23-007-acp-serve-refactor.md.
- [ ] src/tools.rs:103 schemas is 99 lines of pure data (cognitive score 0). Low priority: consider a constant or one schema per tool.
- [ ] Doc coverage is 356 doc lines against 6,255 production lines, and most of the largest functions have none: patch::parse, run_model_loop, serve, read::execute, compaction::material, compaction::next_piece, patch::prepare.
