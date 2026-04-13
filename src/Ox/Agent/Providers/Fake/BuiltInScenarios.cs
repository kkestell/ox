namespace Ox.Agent.Providers.Fake;

/// <summary>
/// Built-in deterministic scenarios for the fake provider. These cover the
/// core TUI interaction paths without requiring external JSON files.
///
/// Each scenario is a static factory so the turn state is fresh on every load.
/// </summary>
internal static class BuiltInScenarios
{
    /// <summary>
    /// All available built-in scenario names.
    /// </summary>
    public static IReadOnlyList<string> Names =>
        ["hello", "long-response", "tool-call", "permission-tool-call", "error", "multi-turn", "compaction"];

    /// <summary>
    /// Looks up a built-in scenario by name. Returns null if not found.
    /// </summary>
    public static FakeScenario? Get(string name) => name switch
    {
        "hello" => Hello(),
        "long-response" => LongResponse(),
        "tool-call" => ToolCall(),
        "permission-tool-call" => PermissionToolCall(),
        "error" => Error(),
        "multi-turn" => MultiTurn(),
        "compaction" => Compaction(),
        _ => null
    };

    /// <summary>
    /// Simplest scenario: one turn, one short text response.
    /// </summary>
    private static FakeScenario Hello() => new()
    {
        Name = "hello",
        Turns =
        [
            new FakeScenarioTurn
            {
                TextChunks = ["Hello! ", "I'm a fake provider. ", "How can I help you today?"],
                InputTokens = 10,
                OutputTokens = 15,
            }
        ]
    };

    /// <summary>
    /// A long streamed response split across many chunks to exercise incremental
    /// rendering, soft-wrap, and scroll behavior.
    /// </summary>
    private static FakeScenario LongResponse() => new()
    {
        Name = "long-response",
        Turns =
        [
            new FakeScenarioTurn
            {
                TextChunks = GenerateLongResponseChunks(),
                InputTokens = 50,
                OutputTokens = 500,
            }
        ]
    };

    /// <summary>
    /// Tool call scenario: the assistant calls read_file, then responds with
    /// a summary. Two turns — one for the tool call, one for the follow-up.
    /// </summary>
    private static FakeScenario ToolCall() => new()
    {
        Name = "tool-call",
        Turns =
        [
            // Turn 1: assistant requests a tool call.
            new FakeScenarioTurn
            {
                ToolCall = new FakeToolCall
                {
                    Name = "read_file",
                    ArgumentsJson = """{"file_path": "hello.txt"}"""
                },
                InputTokens = 20,
                OutputTokens = 30,
            },
            // Turn 2: after seeing the tool result, assistant gives final text.
            new FakeScenarioTurn
            {
                TextChunks = ["The file contains: ", "\"test-sentinel\". ", "That's all!"],
                InputTokens = 60,
                OutputTokens = 20,
            }
        ]
    };

    /// <summary>
    /// Permission-gated tool call: the assistant calls write_file, which
    /// requires user permission. Two turns — tool call then summary.
    /// </summary>
    private static FakeScenario PermissionToolCall() => new()
    {
        Name = "permission-tool-call",
        Turns =
        [
            new FakeScenarioTurn
            {
                ToolCall = new FakeToolCall
                {
                    Name = "write_file",
                    ArgumentsJson = """{"file_path": "output.txt", "content": "hello from fake provider"}"""
                },
                InputTokens = 20,
                OutputTokens = 40,
            },
            new FakeScenarioTurn
            {
                TextChunks = ["I've written the file. ", "The content has been saved to output.txt."],
                InputTokens = 80,
                OutputTokens = 25,
            }
        ]
    };

    /// <summary>
    /// Error scenario: the provider throws on the first turn.
    /// </summary>
    private static FakeScenario Error() => new()
    {
        Name = "error",
        Turns =
        [
            new FakeScenarioTurn
            {
                SimulateError = true,
                ErrorMessage = "Simulated provider failure for testing error paths.",
            }
        ]
    };

    /// <summary>
    /// Multi-turn scenario: three turns of simple conversation to exercise
    /// session persistence and multi-turn rendering.
    /// </summary>
    private static FakeScenario MultiTurn() => new()
    {
        Name = "multi-turn",
        Turns =
        [
            new FakeScenarioTurn
            {
                TextChunks = ["I'm ready to help. ", "What would you like to do?"],
                InputTokens = 10,
                OutputTokens = 12,
            },
            new FakeScenarioTurn
            {
                TextChunks = ["Sure, ", "I can help with that. ", "Here's what I found:"],
                InputTokens = 30,
                OutputTokens = 15,
            },
            new FakeScenarioTurn
            {
                TextChunks = ["All done! ", "Let me know if you need anything else."],
                InputTokens = 50,
                OutputTokens = 12,
            }
        ]
    };

    /// <summary>
    /// Compaction scenario: exercises the two-stage compaction pipeline. The
    /// context window is deliberately tiny (1000 tokens) so the 60% threshold
    /// triggers once the conversation has enough messages.
    ///
    /// The autocompactor requires at least MinPreservedContentMessages + 1 = 6
    /// messages before it will compact. That means we need 4 user turns (8
    /// messages: 4 user + 4 assistant) before compaction can fire on the 5th
    /// turn. Turn 3 (the 4th response) reports 700 input tokens to breach the
    /// 60% threshold. Turn layout:
    ///
    ///   Turn 0–2: normal responses with increasing token counts (ramp up)
    ///   Turn 3:   response with 700 InputTokens — breaches 60% of 1000
    ///   Turn 4:   consumed by autocompactor's non-streaming GetResponseAsync
    ///             (the summary). Not shown to the user.
    ///   Turn 5:   normal response for the 5th user message after compaction.
    /// </summary>
    private static FakeScenario Compaction() => new()
    {
        Name = "compaction",
        ContextWindow = 1000,
        Turns =
        [
            // Turn 0: first response — low tokens, just building up message history.
            new FakeScenarioTurn
            {
                TextChunks = ["Response one. ", "Building up context history."],
                InputTokens = 100,
                OutputTokens = 20,
            },
            // Turn 1: second response.
            new FakeScenarioTurn
            {
                TextChunks = ["Response two. ", "More conversation history."],
                InputTokens = 200,
                OutputTokens = 20,
            },
            // Turn 2: third response.
            new FakeScenarioTurn
            {
                TextChunks = ["Response three. ", "Context is growing."],
                InputTokens = 400,
                OutputTokens = 20,
            },
            // Turn 3: fourth response — 700 tokens breaches the 60% threshold.
            // After this turn, LastInputTokens=700. On the 5th user message,
            // pre-turn compaction sees 700 > 600 and fires.
            new FakeScenarioTurn
            {
                TextChunks = ["Response four. ", "Context is nearly full now."],
                InputTokens = 700,
                OutputTokens = 30,
            },
            // Turn 4: consumed by autocompactor as the summary (non-streaming call).
            new FakeScenarioTurn
            {
                TextChunks = ["Summary: user had a multi-turn conversation about building context history."],
                InputTokens = 100,
                OutputTokens = 30,
            },
            // Turn 5: post-compaction response for the 5th user message.
            new FakeScenarioTurn
            {
                TextChunks = ["After compaction, ", "context was summarized. ", "Continuing normally."],
                InputTokens = 150,
                OutputTokens = 25,
            },
        ]
    };

    /// <summary>
    /// Generates many small text chunks to simulate a long streaming response.
    /// Each paragraph is broken into word-level chunks for realistic streaming.
    /// </summary>
    private static List<string> GenerateLongResponseChunks()
    {
        var paragraphs = new[]
        {
            "This is a long response from the fake provider to test how the TUI handles extended streaming output.",
            "The response is split into many small chunks, each representing a word or short phrase, to simulate realistic token-by-token streaming behavior.",
            "When testing TUI rendering, it's important to verify that soft-wrapping, scroll-to-bottom, and incremental redraw all work correctly with large amounts of text.",
            "This paragraph is here to add more content. The fake provider emits these chunks with no delay between them, which tests the TUI's ability to handle rapid updates.",
            "Final paragraph. The response should render completely without truncation, and the scroll position should follow the latest content as it appears.",
        };

        var chunks = new List<string>();
        foreach (var para in paragraphs)
        {
            // Split paragraph into word-level chunks for realistic streaming.
            var words = para.Split(' ');
            for (var i = 0; i < words.Length; i++)
            {
                chunks.Add(i < words.Length - 1 ? words[i] + " " : words[i]);
            }
            chunks.Add("\n\n");
        }

        return chunks;
    }
}
