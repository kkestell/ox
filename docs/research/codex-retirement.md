# Why Codex support was dropped

> Hypercube's historical decision, not Ox's support policy. See
> [provenance and scope](README.md).

Hypercube removed its bundled Codex harness on November 7, 2026, in
[`0ab5ca7`](https://github.com/kkestell/hypercube/commit/0ab5ca7).
Supporting it meant maintaining a substantial, stateful app-server client,
repeatedly fixing interactions between steering, Stop, and questions, and
choosing between unwanted conversations in the user's normal ChatGPT/Codex
session list and an intrusive storage workaround. The integration's maintenance
cost and complexity were not worth retaining alongside the other harnesses.

This is a historical explanation, not a current integration guide. The evidence
below comes from the deleted plugin, commits, and project records. It does not
claim Codex was impossible to integrate, that every defect was upstream, or
that the final plugin was wholly broken. Several failures were in Hypercube's
own adapter, and native steering and Stop tests passed before removal.

## How much code it took

These are physical lines, including comments and blank lines, counted from
tracked files at `0ab5ca7^`, immediately before removal. Each row covers only
that plugin's directory; shared helpers and core/UI integration are excluded.

| Plugin | Production Go | Test Go | Fixtures/other | Total |
| --- | ---: | ---: | ---: | ---: |
| Codex | 1,594 | 2,423 | 69 | 4,086 |
| Claude Code | 937 | 1,056 | 122 | 2,115 |
| OpenCode | 986 | 1,032 | 0 | 2,018 |
| Pi | 917 | 1,085 | 235 | 2,237 |

Codex had six production Go files, nine test files, and five JSONL fixtures.
Its production adapter was about 1.6–1.7 times the size of each alternative;
the whole directory was roughly twice as large. Line counts are not a quality
score, and capabilities differed, but they show the relative maintenance load.
`main_test.go` alone was 1,416 lines. Native tests added a local Responses
provider, streaming support, a controlled shell process, and an MCP stand-in.

It also had costs outside its directory. Removing Codex allowed deletion of
the shared JSON-RPC helper and tests (187 + 197 lines) and form-schema helper
and tests (82 + 80 lines, retaining the `Notes` function elsewhere). The
Codex-only asynchronous-question distinction disappeared from the plugin
protocol and session reporting. The complete removal commit changed 72 files,
with 4,837 deletions and 475 additions; those totals include documentation,
packaging, and the removal's own plan/work/review, not just executable code.

The final snapshot understates the earlier burden: the private-storage
workaround had already been deleted. Immediately before that deletion in
`da5ea0d`, `storage.go` was 256 lines and its tests were another 378.

## The session-list pollution and its expensive workaround

Hypercube's agent conversations were implementation details of its own session
tree, but native Codex persistence exposed them in the user's normal Recents.
That made the ChatGPT desktop app's session list noisy with Hypercube work
rather than just conversations the user deliberately opened there. Running a
team multiplied the clutter. This was a user-facing cost, not merely untidy
files on disk.

The first attempted answer was `threadSource: "chatgpt_hidden"`. The private
storage investigation established that this field was analytics attribution,
not a visibility control. It did not provide the hiding guarantee the earlier
documentation claimed.

The November 2 fix, [`bb605c1`](https://github.com/kkestell/hypercube/commit/bb605c1),
therefore created a separate `CODEX_HOME` under Hypercube's state directory:

- Override `sqlite_home` too, so linked user configuration could not redirect
  metadata back into the normal database.
- Link an explicit list of settings, credentials, instructions, skills, rules,
  hooks, and plugins. Do not copy credentials, mirror all native storage, change
  `HOME`, or alter the user's global settings.
- Find and copy only a requested older conversation's native rollout, retaining
  its ID and full history rather than silently starting a replacement.
- Investigate credential refresh through symlinks and native resume from copied
  rollouts. The plan also specified preserving rollouts before archiving old,
  inactive Hypercube threads; it was not permission to touch unrelated history.

Account switching then expanded this into source-specific private homes,
hashed source paths, compatibility checks for the old home, searches for private
resumes, and parsing an optional Neuron account-selection script. A resumed
conversation had to retain its original account even after account selection
changed. A request to keep Recents clean had become storage, authentication,
account-selection, and history-import integration.

That approach was deliberately abandoned on November 5 in
[`da5ea0d`](https://github.com/kkestell/hypercube/commit/da5ea0d). Codex again
inherited its native environment and storage, and separate Recents behavior was
dropped. There was no supported cross-home import to rely on. New conversation
references gained a `native:` prefix; old private references failed explicitly
because the native home could contain a stale source copy with the same thread
ID. This prevented resuming the wrong history, but meant the storage workaround
could not simply be removed while preserving automatic resume for those sessions.

The important sequence is **pollution → private-storage workaround → removal
of that workaround → removal of the harness**. The final plugin did not still
contain private-home code or promise clean Recents.

Sources: [private-storage plan](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/plans/2026-11-02-003-codex-private-storage.md),
[account-selection plan](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/plans/2026-11-04-002-codex-account-selection.md), and
[supported-integrations decision](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/plans/2026-11-04-006-supported-integrations.md).

## Why the adapter was difficult to reason about

The persistent `codex app-server` integration arrived November 1 in `3b4176c`.
It was not a command that returned one answer: it was a bidirectional RPC
connection carrying replies, notifications, and server-initiated requests.
Hypercube had to translate that into its own session state and transcript.

The plugin tracked the main thread and turn, in-flight inputs, deferred
interrupts, the last completed turn, final-answer IDs, streamed items, running
tools, completed-item IDs, native child threads and their turns, outstanding
questions, an answer in flight, and sparse account-limit windows. These states
interacted rather than advancing in one simple request/response sequence:

- **Steering could race completion.** A message should steer a running turn,
  but start a new one if the old turn had just ended. A rejected steer did not
  uniquely identify that case, so the adapter checked thread status before
  falling back to `turn/start`.
- **Stop could arrive before a turn ID existed, or during that fallback.**
  Interrupting only the currently known turn could leave the newly started
  work running. Overlapping normal input and an async answer made a single
  pending-input boolean inadequate; the final code counted outstanding calls.
- **Completion was not the end of all events.** Background command completion
  could arrive after the next main turn started. Native children had independent
  turns and could outlive the parent's turn. Child output must not change the
  main agent's idle state, context usage, or final-answer selection.
- **Questions had different lifetimes.** Blocking RPC requests needed structured
  responses or refusal. Async questions arrived inside agent messages and were
  answered through ordinary input/steering. Turn completion must not dismiss
  unanswered async questions, and answering one must not dismiss its siblings.
- **Approval was more than yes/no.** Command, file, and additional-permission
  requests had different response shapes and scopes. MCP elicitation also
  required URL/message handling and typed form conversion. MCP approvals lacked
  stable tool identity, so they could not safely receive the same persistent
  per-tool grants as identifiable command/file requests.

These were real integration responsibilities, not all needless code. The cost
was making them work together and keeping their behavior correct as the adapter
changed. Historical readability reviews also flagged lifecycle handling mixed
with payload conversion in `events.go`; size alone was not the whole problem.

## Representative bugs and fixes

This is a selected history, not a claim that every commit was a separate
upstream defect. Commit IDs can be inspected with `git show <id>`.

| Date | Evidence | Failure or maintenance work |
| --- | --- | --- |
| Oct 31 | `ff77072` | Supply launch-directory trust for Codex agents. |
| Nov 1 | `55943d3` | Simplify requests into plain data, share form helpers, and decide steer fallback from thread status. |
| Nov 2 | `0601676`, `4dd12a0` | State-lifetime changes exposed late shell completions: clearing completed IDs at a new turn allowed an old command to emit again. Preserve the IDs for the process lifetime. |
| Nov 2–5 | `bb605c1`, `42ae1d9`, `da5ea0d` | Isolate Recents, extend private storage for account selection, then delete that unsupported integration. |
| Nov 3 | `8d76414`, `bc8664e` | Add async question controls and dismiss obsolete questions when new input supersedes them. |
| Nov 5 | `b4e604d` | Merge sparse usage-limit updates; omitted window metadata must not erase previously known information. |
| Nov 6 | `cb7c249` | Retain Stop while `turn/start` is awaiting a reply. |
| Nov 6 | `024b853` | Extend that fix across a rejected steer followed by a new turn. |
| Nov 6 | `5ee6d6c` | Fix overlapping input/async-answer lifetimes: one completed call could clear another call's pending interrupt, letting fallback work escape Stop. |
| Nov 7 | `c05f253` | Preserve unanswered async questions after turn completion and preserve sibling questions when answering one. |
| Nov 7 | `e36f05b` | Add installed-Codex smoke tests proving steering keeps a tool running and Stop terminates it. |
| Nov 7 | `0ab5ca7` | Remove the harness and machinery with no remaining production caller. |

The [state-lifetime review](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/reviews/2026-11-02-003-plugin-state-lifetimes.md)
documents the late-completion reproduction. The
[whole-project review](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/reviews/2026-11-06-003-whole-project.md#3-give-overlapping-codex-input-calls-a-coherent-lifetime)
records a deterministic Stop escape: an old steer remained outstanding, another
input completed, Stop targeted the old turn, and the held steer then fell back
to a new turn that kept running. These were confirmed bugs, not hypothetical
races. The [native smoke-test review](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/reviews/2026-11-07-013-codex-native-smoke.md)
records successful checks with `codex-cli 0.167.0` and a local provider, without
paid model calls. Passing those tests is compatible with deciding the ongoing
support burden was too high.

## What removal means

Hypercube bundles Claude Code, OpenCode, and Pi instead. Codex's capabilities
did not justify retaining its adapter, exclusive shared helpers, special
question semantics, packaging, and presentation support. Dropping the harness
also avoided rebuilding the discarded private-storage workaround merely to
make its session-list behavior acceptable again.

Removal did **not** delete the user's native Codex executable, credentials,
settings, native history, or saved Hypercube sessions and transcripts. Old
records remain readable; Hypercube no longer launches, resumes, or converts
those conversations. See [stored-data behavior](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/docs/storage.md) and the
[removal plan](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/plans/2026-11-07-012-remove-codex-integration.md),
[work log](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/work/2026-11-07-009-remove-codex-integration.md), and
[review](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/reviews/2026-11-07-014-remove-codex-integration.md).

The old [Codex research](harnesses/codex.md) is retained as
version-specific background, not a promise of support. Any future proposal to
restore the harness should address these demonstrated costs, especially native
history visibility and coherent steering/Stop/question lifetimes, rather than
assuming the old plugin only needed to be copied back.
