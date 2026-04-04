using Ur.Console;
using Ur.Drawing;
using Ur.Widgets;

namespace Ur.Demo;

/// <summary>
/// Chat UI demo showing ListView, ScrollView, TextInput, and heterogeneous message
/// widgets working together. A background task simulates incoming messages of
/// different types (user, system, tool) via Application.Invoke() so all widget
/// mutation stays on the UI thread.
///
/// Layout:
///   [ title label                        ]
///   [ ScrollView (grows to fill)         ]
///   [   ListView<ChatMessage>            ]
///   [     UserMessageWidget              ]
///   [     SystemMessageWidget            ]
///   [     ToolMessageWidget              ]
///   [ TextInput (focusable)              ]
///   [ hint label                         ]
/// </summary>
class ChatDemoApp : Application
{
    private readonly ListView<ChatMessage> _listView;

    public ChatDemoApp()
    {
        _listView = new ListView<ChatMessage>(msg => msg switch
        {
            UserMessage m   => new UserMessageWidget(m),
            SystemMessage m => new SystemMessageWidget(m),
            ToolMessage m   => new ToolMessageWidget(m),
            _               => new Label(msg.Content),
        });

        var scrollView = new ScrollView(_listView) { AutoScroll = true };

        var root = Flex.Vertical();
        root.HorizontalSizing = SizingMode.Grow;
        root.VerticalSizing = SizingMode.Grow;

        root.AddChild(new Label("Ox Chat Demo  (↑/↓ to scroll, Ctrl-C to quit)")
        {
            Style = new Style(Color.BrightWhite, Color.Black, Modifier.Bold),
        });

        root.AddChild(scrollView);

        // Input row: a growing text field with a Send button to its right.
        // Flex.Horizontal() places them side-by-side; the TextInput grows to
        // fill remaining width while the Button stays its natural label size.
        var textInput = new TextInput
        {
            HorizontalSizing = SizingMode.Grow
        };

        var sendButton = new Button("Send");
        sendButton.Clicked += () =>
        {
            if (string.IsNullOrWhiteSpace(textInput.Value)) return;
            _listView.Items.Add(new UserMessage("You", textInput.Value));
            textInput.Value = "";
        };

        var inputRow = Flex.Horizontal();
        inputRow.HorizontalSizing = SizingMode.Grow;
        inputRow.AddChild(textInput);
        inputRow.AddChild(sendButton);
        root.AddChild(inputRow);

        root.AddChild(new Label("Tab to switch focus  ·  ↑/↓ to scroll  ·  Ctrl-C to quit")
        {
            Style = new Style(Color.BrightBlack, Color.Black),
        });

        Root = root;
    }

    /// <summary>   
    /// Starts a background task that simulates a multi-participant chat session.
    /// Messages are posted to the UI thread via Invoke() at irregular intervals
    /// to mimic real conversation pacing and demonstrate auto-scroll behavior.
    /// </summary>
    public void StartMessageSimulator()
    {
        var messages = new ChatMessage[]
        {
            new SystemMessage("Alice has joined the channel"),
            new UserMessage("Alice",   "Hey everyone, what's the status?"),
            new SystemMessage("Bob has joined the channel"),
            new UserMessage("Bob",     "Just finishing up the last test run."),
            new ToolMessage("run_tests",
                "PASS  Ur.Tests (107 tests, 0 failures)\nPASS  Ur.Drawing.Tests (12 tests)\nAll tests passed."),
            new UserMessage("Alice",   "Nice, green across the board!"),
            new UserMessage("Bob",     "Yep. Deploying to staging now."),
            new ToolMessage("deploy",
                "Deploying rev e5b6e3c to staging...\nHealth checks passed.\nDeployment complete."),
            new SystemMessage("Charlie has joined the channel"),
            new UserMessage("Charlie", "Sorry I'm late — did I miss anything?"),
            new UserMessage("Alice",   "Just deployment. All good."),
            new UserMessage("Bob",     "We should add more tool tests."),
            new ToolMessage("search_code",
                "Found 3 references to RenderWidget:\n  Renderer.cs:17\n  ScrollView.cs:88\n  Renderer.cs:47"),
            new UserMessage("Alice",   "Great, I'll open a ticket."),
            new SystemMessage("Alice has left the channel"),
            new UserMessage("Charlie", "See you all tomorrow!"),
            new UserMessage("Bob",     "👋"),
        };

        Task.Run(async () =>
        {
            // Stagger messages with short delays to demonstrate auto-scroll tracking
            // new content and the pause-on-scroll-up behavior.
            foreach (var msg in messages)
            {
                await Task.Delay(700);
                Invoke(() => _listView.Items.Add(msg));
            }

            // After the scripted messages, keep adding periodic status updates so
            // the user can see auto-scroll resume after scrolling back down.
            var tick = 0;
            while (true)
            {
                await Task.Delay(3000);
                tick++;
                Invoke(() => _listView.Items.Add(
                    new SystemMessage($"Heartbeat #{tick} — system nominal")));
            }
        });
    }
}

public class Program
{
    public static void Main(string[] args)
    {
        var app = new ChatDemoApp();
        app.StartMessageSimulator();
        app.Run(new ConsoleDriver());
    }
}
