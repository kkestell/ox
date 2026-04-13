using System.Runtime.InteropServices;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Ur.Compaction;
using Ur.Configuration;
using Ur.Configuration.Keyring;
using Ur.Providers;
using Ur.Sessions;
using Ur.Settings;
using Ur.Skills;
using Ur.Tools;

namespace Ur.Hosting;

/// <summary>
/// Registers all Ur application-scoped services into the DI container.
///
/// Every service is registered as a singleton via factory delegates so the container
/// resolves them in dependency order. Per-session and per-turn objects (UrSession,
/// AgentLoop, ToolRegistry, SubagentRunner) stay as procedural construction because
/// they need per-call parameters (session ID, chat client, callbacks, etc.).
///
/// The host is responsible for logging, configuration sources, and application lifecycle.
/// Call <see cref="AddUrSettings"/> on the <see cref="IConfigurationBuilder"/> before
/// calling <see cref="AddUr"/> so that user and workspace settings files are part of
/// the host's configuration root. <see cref="UrOptions"/> is bound to the "ox" section
/// via the standard <c>Configure&lt;T&gt;</c> pipeline.
/// </summary>
public static class ServiceCollectionExtensions
{
    /// <summary>
    /// Registers the user-level and workspace-level settings files as configuration
    /// sources. Call this on the host's <see cref="IConfigurationBuilder"/> before
    /// <see cref="AddUr"/> so that <see cref="IConfigurationRoot.Reload"/> propagates
    /// changes through the standard options pipeline.
    ///
    /// Two <see cref="UrSettingsConfigurationSource"/> instances are added: user file
    /// first, workspace file second. IConfiguration's "last source wins" rule means
    /// workspace values override user values for the same key.
    /// </summary>
    public static IConfigurationBuilder AddUrSettings(
        this IConfigurationBuilder builder,
        string userSettingsPath,
        string workspaceSettingsPath)
    {
        builder.Add(new UrSettingsConfigurationSource(userSettingsPath));
        builder.Add(new UrSettingsConfigurationSource(workspaceSettingsPath));
        return builder;
    }

    /// <summary>
    /// Registers Ur services into the DI container. The host must provide
    /// <paramref name="configuration"/> (which should already include Ur settings
    /// sources via <see cref="AddUrSettings"/>). Logging is the host's responsibility
    /// — this method does not configure logging providers.
    ///
    /// The optional <paramref name="configure"/> callback sets runtime values
    /// (WorkspacePath, SelectedModelOverride, etc.) after file-based settings
    /// are bound from IConfiguration. Code overrides win over file values.
    ///
    /// To override the keyring, register <c>IKeyring</c> before calling this method —
    /// <c>AddUr</c> uses <c>TryAddSingleton</c> so pre-registered keyrings win.
    /// Similarly, register <c>IProvider</c> instances (e.g. <see cref="Ur.Providers.Fake.FakeProvider"/>)
    /// before calling AddUr for test/dev scenarios.
    /// </summary>
    public static IServiceCollection AddUr(
        this IServiceCollection services,
        IConfiguration configuration,
        Action<UrOptions>? configure = null)
    {
        // Bind the "ox" section to UrOptions via the standard options pipeline.
        // ConfigurationBinder.Bind matches property names case-insensitively:
        //   Model ← "ox:model", TurnsToKeepToolResults ← "ox:turnsToKeepToolResults".
        // Defaults (e.g. TurnsToKeepToolResults = 3) are preserved when keys are absent.
        services.Configure<UrOptions>(configuration.GetSection("ox"));

        // The configure callback runs after config-file binding, so code overrides
        // (WorkspacePath, SelectedModelOverride, etc.) win over file values.
        if (configure is not null)
            services.PostConfigure(configure);

        // Ensure IConfigurationRoot is available in DI for SettingsWriter.Reload().
        // Host.CreateApplicationBuilder registers IConfiguration but not IConfigurationRoot
        // separately. The host's ConfigurationManager implements both interfaces.
        if (configuration is IConfigurationRoot configRoot)
            services.AddSingleton(configRoot);

        // Snapshot the options for values needed at registration time (paths).
        // PostConfigure callbacks haven't run yet, so we apply them manually.
        var snapshot = new UrOptions();
        configuration.GetSection("ox").Bind(snapshot);
        configure?.Invoke(snapshot);

        var userDataDirectory = snapshot.UserDataDirectory ?? DefaultUserDataDirectory();
        var userSettingsPath = snapshot.UserSettingsPath ?? DefaultUserSettingsPath(userDataDirectory);

        services.AddSingleton(_ =>
        {
            var w = new Workspace(snapshot.WorkspacePath);
            w.EnsureDirectories();
            return w;
        });

        // TryAddSingleton lets callers pre-register a custom IKeyring (e.g. test
        // keyrings, EnvironmentKeyring for headless) before calling AddUr.
        services.TryAddSingleton<IKeyring>(_ => CreatePlatformKeyring());

        services.AddSingleton<ISessionStore>(sp =>
            new JsonlSessionStore(
                sp.GetRequiredService<Workspace>().SessionsDirectory,
                sp.GetRequiredService<ILoggerFactory>().CreateLogger<JsonlSessionStore>()));

        // Compaction strategy — the default Autocompactor summarizes older messages
        // when context fill exceeds 60%. Registered as a singleton because the strategy
        // is stateless (the chat client and messages vary per call, not per registration).
        services.AddSingleton<ICompactionStrategy, Autocompactor>();

        services.AddSingleton(_ =>
        {
            var registry = new SettingsSchemaRegistry();
            RegisterCoreSchemas(registry);
            return registry;
        });

        // SettingsWriter validates against the schema registry, writes nested JSON,
        // and triggers IConfigurationRoot.Reload() so IOptionsMonitor picks up changes.
        // It resolves IConfigurationRoot from DI — this is the host's configuration root
        // which includes the UrSettingsConfigurationSource instances added by AddUrSettings.
        services.AddSingleton(sp =>
        {
            var workspace = sp.GetRequiredService<Workspace>();
            var logger = sp.GetRequiredService<ILoggerFactory>().CreateLogger(nameof(SettingsWriter));
            return new SettingsWriter(
                sp.GetRequiredService<SettingsSchemaRegistry>(),
                sp.GetRequiredService<IConfigurationRoot>(),
                userSettingsPath,
                workspace.SettingsPath,
                logger);
        });

        services.AddSingleton(sp =>
        {
            var workspace = sp.GetRequiredService<Workspace>();
            var logger = sp.GetRequiredService<ILoggerFactory>().CreateLogger(nameof(SkillLoader));
            var skills = SkillLoader.LoadAll(
                Path.Combine(userDataDirectory, "skills"), workspace.SkillsDirectory, logger);
            return new SkillRegistry(skills);
        });

        // BuiltInCommandRegistry is a fixed, code-defined list of first-party slash commands.
        // CommandRegistry merges built-ins and skills into the single ordered list that
        // autocomplete uses for prefix matching. Both are immutable after construction.
        services.AddSingleton<BuiltInCommandRegistry>();
        services.AddSingleton(sp => new CommandRegistry(
            sp.GetRequiredService<BuiltInCommandRegistry>(),
            sp.GetRequiredService<SkillRegistry>()));

        // Provider registry — collects all IProvider instances from DI and maps them
        // by name prefix. The host (Ox) registers providers before calling AddUr —
        // either from ProviderConfig (providers.json) or directly (FakeProvider).
        services.AddSingleton(sp =>
        {
            var registry = new ProviderRegistry();
            foreach (var provider in sp.GetServices<IProvider>())
                registry.Register(provider);
            return registry;
        });

        services.AddSingleton(sp => new UrConfiguration(
            sp.GetRequiredService<IOptionsMonitor<UrOptions>>(),
            sp.GetRequiredService<SettingsWriter>(),
            sp.GetRequiredService<IKeyring>(),
            sp.GetRequiredService<ProviderRegistry>()));

        // UrHost: registered via factory because the constructor is internal
        // (UrHost is a public type but we don't want arbitrary external construction).
        // The Func<string, int?> is an optional context window resolver provided by
        // the host (Ox registers it from OxConfiguration.ResolveContextWindow).
        services.AddSingleton(sp => new UrHost(
            sp.GetRequiredService<Workspace>(),
            sp.GetRequiredService<ISessionStore>(),
            sp.GetRequiredService<ICompactionStrategy>(),
            sp.GetRequiredService<SkillRegistry>(),
            sp.GetRequiredService<BuiltInCommandRegistry>(),
            sp.GetRequiredService<SettingsSchemaRegistry>(),
            sp.GetRequiredService<UrConfiguration>(),
            sp.GetRequiredService<ProviderRegistry>(),
            sp.GetRequiredService<ILoggerFactory>(),
            sp.GetRequiredService<IOptionsMonitor<UrOptions>>(),
            userDataDirectory,
            sp.GetService<Func<string, IChatClient>>(),
            sp.GetService<ToolRegistry>(),
            sp.GetService<Func<string, int?>>()));

        return services;
    }

    // --- Helpers migrated from UrHost ---

    internal static IKeyring CreatePlatformKeyring()
    {
        if (RuntimeInformation.IsOSPlatform(OSPlatform.OSX))
            return new MacOsKeyring();
        return RuntimeInformation.IsOSPlatform(OSPlatform.Linux)
            ? new LinuxKeyring()
            : throw new PlatformNotSupportedException("Ur requires macOS or Linux.");
    }

    internal static void RegisterCoreSchemas(SettingsSchemaRegistry registry)
    {
        var stringSchema = System.Text.Json.JsonDocument.Parse("""{"type":"string"}""")
            .RootElement.Clone();
        registry.Register(UrConfiguration.ModelSettingKey, stringSchema);
    }

    internal static string DefaultUserDataDirectory() =>
        Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".ox");

    internal static string DefaultUserSettingsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "settings.json");
}
