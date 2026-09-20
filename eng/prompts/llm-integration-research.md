# LLM integration research prompt

## Inputs

- Project: `<owner/repository>`
- Repository URL: `<https://github.com/owner/repository>`
- Revision: `<full commit SHA>`
- Source checkout: `<absolute path to the checkout>`
- Report path: `eng/research/<owner>--<repository>.md`

## Prompt

Inspect **Project** at exactly **Revision** in **Source checkout**, then update
the existing ACP architecture report at **Report path**. Do not modify the
source checkout and do not rewrite unrelated report sections.

Add a `## LLM abstraction and integration` section immediately after `###
Runtime and process boundaries` and before `## Session model`. Use these
subheadings exactly:

### Abstraction

State whether the project uses a named third-party LLM abstraction library, a
provider SDK directly, a bespoke internal abstraction, or a wrapped
executable/service. Name the library, SDK, or internal module precisely. For a
wrapped runtime, name the boundary and say that the model loop is outside this
repository. Support each claim with repository-relative `path:line` citations.

### Integration path

Trace a normal prompt from history/message conversion through provider or model
selection, request construction, streaming, tool-call handling, and conversion
back to runtime events. For an adapter or gateway, trace to the delegated
boundary and state what is not visible. Keep this focused on the model
integration, not general ACP mechanics.

### Provider and tool boundary

Explain where provider-specific code, credentials/configuration, request and
response normalization, and tool schemas live. State whether the abstraction
is multi-provider and how a provider is selected. If a detail is not visible,
write **Not found** and identify the checked files.

### Limits

State material facts that cannot be determined from the checkout; write `None`
only if no such boundary exists.

Research from implementation evidence. Locate dependencies and imports, then
the actual model-call site, streaming consumer, provider registry or factory,
and tool loop. Follow calls far enough to distinguish an active abstraction
from a manifest-only dependency. Do not rely on README claims, package manifests,
or generated types alone. Label a conclusion **Inference** when necessary;
otherwise do not guess. Preserve the report's existing source-grounded style
and update its evidence index with the key new locations. Before finishing,
verify the section is in the specified location, the project URL and revision
still match the supplied inputs, all statements have implementation evidence,
and no placeholder text remains.
