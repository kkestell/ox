using System.Collections.ObjectModel;
using Ur.Console;
using Ur.Drawing;
using Ur.Widgets;

namespace Ur.Demo;

/// <summary>
/// Simple record representing a model option in the model picker dialog.
/// Each row has a display name, provider, and context window size.
/// </summary>
record ModelOption(string Name, string Provider, string ContextWindow);

/// <summary>
/// A dialog that prompts for an OpenRouter API key.
/// Demonstrates subclassing Dialog to build custom forms: add widgets
/// to the protected Content area and expose accessors for the caller
/// to read values after the dialog closes.
/// </summary>
class ApiKeyDialog : Dialog
{
    private readonly TextInput _apiKeyInput;

    public ApiKeyDialog() : base("OpenRouter API Key")
    {
        Content.AddChild(new Label("Enter your OpenRouter API Key:"));

        _apiKeyInput = new TextInput { HorizontalSizing = SizingMode.Grow };
        Content.AddChild(_apiKeyInput);
    }

    /// <summary>
    /// The value entered by the user. Read this after Closed fires with OK.
    /// </summary>
    public string ApiKey => _apiKeyInput.Value;
}

/// <summary>
/// A dialog that presents a scrollable table of model options for the user to
/// pick from. Demonstrates the Table{T} widget with row selection, scroll-to-center,
/// and activation-on-Enter. Populated with 100 dummy rows to exercise scrolling.
/// </summary>
class ModelDialog : Dialog
{
    private readonly Table<ModelOption> _table;

    // Rotating provider names and context sizes for dummy data generation.
    private static readonly string[] Providers = ["OpenAI", "Anthropic", "Google", "Meta", "Mistral"];
    private static readonly string[] ContextSizes = ["4K", "8K", "16K", "32K", "128K", "200K", "1M"];

    public ModelDialog() : base("Select Model")
    {
        // The Dialog's Content area defaults to Fit sizing, which works for labels
        // and text inputs that have a natural height. For growable content like a
        // Table, switch to Grow so the Flex takes the constrained height from Dialog
        // and passes it through to the Table.
        Content.VerticalSizing = SizingMode.Grow;

        var dataSource = new ObservableCollection<ModelOption>();

        // Populate with 100 dummy rows, rotating through providers and context sizes
        // to give the table varied data for visual testing.
        for (var i = 0; i < 100; i++)
        {
            dataSource.Add(new ModelOption(
                $"model-{i}",
                Providers[i % Providers.Length],
                ContextSizes[i % ContextSizes.Length]));
        }

        var columns = new List<TableColumn<ModelOption>>
        {
            new("Model Name", m => m.Name),
            new("Provider", m => m.Provider),
            new("Context Window", m => m.ContextWindow),
        };

        _table = new Table<ModelOption>(dataSource, columns);

        // Enter on a row dismisses the picker immediately — the consumer reads
        // SelectedModel to find out what was chosen.
        _table.ItemActivated += _ => Close(DialogResult.OK);

        Content.AddChild(_table);
    }

    /// <summary>
    /// The model the user selected, or null if the dialog was cancelled or the
    /// table was empty. Read this after Closed fires.
    /// </summary>
    public ModelOption? SelectedModel =>
        _table.SelectedIndex >= 0 ? _table.DataSource[_table.SelectedIndex] : null;
}

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
///   [ [TextInput] [Send]                 ]
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
            // Open an API key dialog instead of posting a message. This
            // demonstrates the modal dialog system — the dialog overlays
            // the chat UI and captures all input until dismissed.
            var dialog = new ApiKeyDialog();
            dialog.Closed += result =>
            {
                if (result == DialogResult.OK && !string.IsNullOrWhiteSpace(dialog.ApiKey))
                {
                    _listView.Items.Add(new SystemMessage($"API key set: {dialog.ApiKey[..4]}****"));
                }
                else
                {
                    _listView.Items.Add(new SystemMessage("API key dialog cancelled."));
                }
            };
            ShowModal(dialog);
        };

        var modelButton = new Button("Model");
        modelButton.Clicked += () =>
        {
            var dialog = new ModelDialog();
            dialog.Closed += result =>
            {
                if (result == DialogResult.OK && dialog.SelectedModel is { } model)
                {
                    _listView.Items.Add(new SystemMessage($"Selected model: {model.Name} ({model.Provider}, {model.ContextWindow})"));
                }
            };
            ShowModal(dialog);
        };

        var inputRow = Flex.Horizontal();
        inputRow.HorizontalSizing = SizingMode.Grow;
        inputRow.AddChild(textInput);
        inputRow.AddChild(sendButton);
        inputRow.AddChild(modelButton);
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
