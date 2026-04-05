namespace Ur;

/// <summary>
/// All context a tool factory might need to construct a tool instance.
///
/// This is a union-of-concerns record, not a god object. The design tradeoff is
/// deliberate: a single factory signature (<see cref="Tools.ToolFactory"/>) means the
/// registration loop has zero special-casing, and adding new context tiers requires
/// only a new field here — not a new factory type or registration overload.
///
/// Tools use only the fields they need and ignore the rest. That's acceptable
/// because the cognitive simplicity of one factory shape is worth more than the
/// minor overhead of passing unused context.
///
/// Currently carries workspace and session context needed by the initial set of
/// builtin tools. Future tools (e.g. SubagentTool) will extend this with
/// ChatClient, tool registry access, callbacks, and system prompt without
/// changing the factory delegate signature.
/// </summary>
internal record ToolContext(
    Workspace Workspace,
    string SessionId);
