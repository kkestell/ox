namespace Ox.Agent.Compaction;

/// <summary>
/// Provides the system prompt used by the autocompactor when asking the LLM to
/// summarize a conversation. The prompt is structured into five sections that
/// capture the most important context for continuing the conversation after
/// compaction. Isolated here for testability and to keep Autocompactor focused
/// on orchestration rather than prompt wording.
/// </summary>
internal static class SummaryPrompt
{
    /// <summary>
    /// Returns the system prompt instructing the LLM to produce a structured
    /// conversation summary. The summary replaces older messages so the model
    /// retains key context without the full token cost of the original history.
    /// </summary>
    public static string Build() =>
        """
        You are a conversation summarizer. Your job is to produce a concise, structured
        summary of the conversation so far. This summary will replace the older messages
        in the context window, so it must capture everything needed to continue the
        conversation without losing important context.

        Organize your summary into these sections (omit any section that has no content):

        ## Primary Request and Intent
        What the user originally asked for and what they are trying to accomplish.

        ## Key Files and Code Changes
        Files that were read, created, or modified. Summarize what was done to each file
        and why. Include file paths.

        ## Errors and Fixes
        Any errors encountered during the session and how they were resolved.

        ## Current Work / Pending Tasks
        What is currently being worked on, including any partially completed tasks or
        next steps that were discussed but not yet started.

        ## User Messages
        Key decisions, preferences, or instructions the user expressed that should
        inform future responses.

        Rules:
        - Be concise but thorough. Prefer bullet points over prose.
        - Include specific file paths, function names, and error messages — not vague references.
        - Do NOT use tool calls. Output text only.
        - Do NOT include conversational filler or meta-commentary about the summarization task.
        """;
}
