# ACP coding-agent architecture research prompt

Use this prompt once per ACP coding-agent repository. Replace every value in
`Inputs` before assigning it to a researcher.

## Inputs

- Project: `<owner/repository>`
- Repository URL: `<https://github.com/owner/repository>`
- Revision: `<full commit SHA>`
- Source checkout: `<absolute path to the checkout>`
- Report path: `eng/research/<owner>--<repository>.md`
- Report template: `eng/prompts/acp-agent-report-template.md`

## Prompt

You are researching one coding agent that exposes an Agent Client Protocol
(ACP) server. The coding agent is the subject; an editor, IDE, or other ACP
client is an external actor. Produce a source-grounded architecture report that
can be compared mechanically and editorially with reports about other agents.

Analyze **Project** at the exact **Revision** in **Source checkout**. Write the
finished report to **Report path**, using **Report template** without changing
its headings, table columns, or controlled vocabulary. Replace all template
instructions and placeholders. Do not modify the source checkout.

The central questions are:

1. Where does ACP terminate, and how is the protocol layer connected to the
   agent runtime, model loop, tools, and storage?
2. What does the project call a session, which component owns it, and how does
   an ACP session ID map to runtime and durable state?
3. Can different sessions run concurrently? Can one session process concurrent
   prompts? Identify the synchronization mechanism and its scope.
4. What is durable, when is it committed, and what is reconstructed on load or
   resume?
5. How does one new prompt flow from the ACP client through the agent and back
   as live ACP updates?
6. How do stored events or messages flow back to the ACP client during session
   loading or replay?
7. How are ACP inputs and live agent events converted into the durable
   representation? Where can ordering, cancellation, failure, retry, or
   backpressure alter that flow?

### Research method

Work from implementation evidence, not feature claims. First locate the ACP
entry point and protocol handlers. Then trace these concrete paths end to end:

1. server initialization and capability negotiation;
2. new-session creation;
3. loading or resuming an existing session;
4. a prompt that emits assistant text and at least one tool call, when tools are
   supported;
5. cancellation or failure during a prompt;
6. a subsequent prompt that consumes prior durable history.

Follow types and calls across files far enough to identify the actual owner of
state. Inspect schemas, serialization types, task spawning, locks, channels,
registries, subprocess boundaries, and cleanup paths that materially affect
sessions or events. Ignore unrelated UI, provider, packaging, and tool details.
Do not infer architecture from filenames alone.

If ACP is only an adapter over another executable or SDK, describe both sides
of that boundary. State which process owns the session, who persists history,
and whether the adapter translates, buffers, or merely forwards events.

### Evidence rules

- Pin the report to the supplied full commit SHA. Do not report behavior from a
  different branch or from current documentation when it disagrees with code.
- Support every material claim with a repository-relative `path:line` citation
  and a symbol when one exists, for example
  `` `src/acp.rs:120` (`handle_prompt`) ``.
- Use line numbers from the pinned revision. Add the most important locations
  to the evidence index; do not turn every sentence into a citation list.
- Label a conclusion **Inference** when the code strongly implies it but does
  not state or exercise it directly. Explain the evidence and uncertainty.
- Use **Not found** when targeted searches found no implementation, and list
  where you looked. Use **Not applicable** only when the concept truly does not
  apply. Never silently fill gaps with likely behavior.
- Treat tests as corroboration, not as a substitute for reading the production
  path. Note meaningful disagreement between tests, docs, and implementation.
- Do not award features based only on README text, configuration examples,
  generated protocol types, dependencies, or dead code.

### Consistency rules

- Preserve every template section. If evidence is absent, keep the section and
  say **Not found**.
- Use only the allowed values shown in the template front matter and summary
  table. Choose `unknown` rather than inventing a new category. Explain nuance
  in prose.
- In yes/no matrices use only `yes`, `partial`, `no`, `unknown`, or `n/a`.
- Distinguish these three things throughout the report:
  - live ACP notifications sent to the client;
  - the agent runtime's in-memory events or messages;
  - durable records used for replay or future model context.
- Distinguish concurrency across different sessions from concurrency within one
  session. Do not treat async I/O or task spawning by itself as proof of safe
  concurrent execution.
- Separate observed implementation from recommendations. This report is a
  description, not a review; put possible lessons only in the final section.
- Keep the report focused and concrete. Prefer tables and short traces over a
  tour of the repository. The completed report should normally be 1,500–3,000
  words.

Before finishing, verify that the report contains the exact repository URL and
revision, all placeholders are gone, all required sections remain, every
controlled field is valid, both concurrency scopes are answered, and the two
event directions are traced separately.
