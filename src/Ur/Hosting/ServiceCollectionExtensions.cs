using System.Runtime.InteropServices;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Ur.Configuration;
using Ur.Configuration.Keyring;
using Ur.Logging;
using Ur.Providers;
using Ur.Sessions;
using Ur.Settings;
using Ur.Skills;

namespace Ur.Hosting;

/// <summary>
/// Registers all Ur application-scoped services into the DI container.
///
/// Every service is registered as a singleton via factory delegates so the container
/// resolves them in dependency order. Per-session and per-turn objects (UrSession,
/// AgentLoop, ToolRegistry, SubagentRunner) stay as procedural construction because
/// they need per-call parameters (session ID, chat client, callbacks, etc.).
///
/// Configuration is backed by two <see cref="UrSettingsConfigurationSource"/> instances
/// (user-level and workspace-level) feeding into <see cref="IConfiguration"/>. The
/// workspace source is added second so its values take priority ("last source wins").
/// <see cref="UrOptions"/> is bound to the "ur" section for strongly-typed access.
/// </summary>
public static class ServiceCollectionExtensions
{
    public static IServiceCollection AddUr(
        this IServiceCollection services,
        UrStartupOptions options)
    {
        // Custom file logger writes to ~/.ur/logs/ur-{date}.log.
        // ClearProviders() removes the console and debug loggers that
        // Host.CreateApplicationBuilder registers by default — without this,
        // those providers write directly to stdout and corrupt the TUI.
        services.AddLogging(builder =>
        {
            builder.ClearProviders();
            builder.AddProvider(new UrFileLoggerProvider());
        });

        // Store options so other registrations and UrHost can access overrides.
        services.AddSingleton(options);

        services.AddSingleton(_ =>
        {
            var w = new Workspace(options.WorkspacePath);
            w.EnsureDirectories();
            return w;
        });

        services.AddSingleton<IKeyring>(_ =>
            options.KeyringOverride ?? CreatePlatformKeyring());

        var userDataDirectory = options.UserDataDirectory ?? DefaultUserDataDirectory();
        var userSettingsPath = options.UserSettingsPath ?? DefaultUserSettingsPath(userDataDirectory);

        // ── IConfiguration pipeline ──────────────────────────────────────
        //
        // Two UrSettingsConfigurationSource instances: user file first, workspace
        // file second. IConfiguration's "last source wins" rule means workspace
        // values override user values for the same key — matching the old merge
        // semantics from Settings/SettingsLoader.
        services.AddSingleton<IConfigurationRoot>(sp =>
        {
            var workspace = sp.GetRequiredService<Workspace>();
            var builder = new ConfigurationBuilder();
            builder.Add(new UrSettingsConfigurationSource(userSettingsPath));
            builder.Add(new UrSettingsConfigurationSource(workspace.SettingsPath));
            return builder.Build();
        });

        // IConfiguration delegates to the root — consumers that only need reads
        // can depend on this interface instead of the concrete root.
        services.AddSingleton<IConfiguration>(sp =>
            sp.GetRequiredService<IConfigurationRoot>());

        // Bind the "ur" section to UrOptions for strongly-typed access to core
        // settings. UrOptionsMonitor reads from IConfiguration on every access,
        // so it always reflects the latest state after SettingsWriter triggers a reload.
        services.AddSingleton<IOptionsMonitor<UrOptions>>(sp =>
            new UrOptionsMonitor(sp.GetRequiredService<IConfiguration>()));

        services.AddSingleton(sp =>
            new SessionStore(
                sp.GetRequiredService<Workspace>().SessionsDirectory,
                sp.GetRequiredService<ILoggerFactory>().CreateLogger<SessionStore>()));

        services.AddSingleton(_ =>
        {
            var registry = new SettingsSchemaRegistry();
            RegisterCoreSchemas(registry);
            return registry;
        });

        // SettingsWriter replaces the old Settings class for read/write operations.
        // It validates against the schema registry, writes nested JSON, and triggers
        // an IConfigurationRoot.Reload() so IOptionsMonitor picks up changes.
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

        // Provider registry — maps provider name prefixes to IProvider implementations.
        //
        // Providers are instantiated from providers.json configuration. Each entry
        // declares the provider type (openai-compatible, google, ollama) and optional
        // endpoint URL. The config-driven loop replaces the old hardcoded per-provider
        // DI registrations.
        var providersJsonPath = options.ProvidersJsonPath
            ?? Path.Combine(userDataDirectory, "providers.json");
        var providerConfig = ProviderConfig.Load(providersJsonPath);
        services.AddSingleton(providerConfig);

        foreach (var name in providerConfig.ProviderNames)
        {
            var entry = providerConfig.GetEntry(name)!;

            switch (entry.Type)
            {
                case "openai-compatible":
                    var endpoint = entry.Endpoint;
                    services.AddSingleton<IProvider>(sp =>
                        new OpenAiCompatibleProvider(
                            name, endpoint, sp.GetRequiredService<IKeyring>()));
                    break;

                case "google":
                    services.AddSingleton<IProvider>(sp =>
                        new GoogleProvider(sp.GetRequiredService<IKeyring>()));
                    break;

                case "ollama":
                    var ollamaUri = entry.Endpoint ?? new Uri("http://localhost:11434");
                    services.AddSingleton<IProvider>(
                        new OllamaProvider(name, ollamaUri));
                    break;

                default:
                    // Unknown provider type — skip silently. The user may have
                    // a newer providers.json format than this version of Ur supports.
                    break;
            }
        }

        // If startup options request a fake provider, register it as an additional
        // IProvider service. The registry will pick it up alongside the real providers.
        if (options.FakeProvider is { } fakeProvider)
            services.AddSingleton(fakeProvider);

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
            sp.GetRequiredService<ProviderRegistry>(),
            sp.GetRequiredService<ProviderConfig>(),
            options.SelectedModelOverride));

        // UrHost: registered via factory because the constructor is internal
        // (UrHost is a public type but we don't want arbitrary external construction).
        services.AddSingleton(sp => new UrHost(
            sp.GetRequiredService<Workspace>(),
            sp.GetRequiredService<SessionStore>(),
            sp.GetRequiredService<SkillRegistry>(),
            sp.GetRequiredService<BuiltInCommandRegistry>(),
            sp.GetRequiredService<SettingsSchemaRegistry>(),
            sp.GetRequiredService<UrConfiguration>(),
            sp.GetRequiredService<ProviderRegistry>(),
            sp.GetRequiredService<ProviderConfig>(),
            sp.GetRequiredService<ILoggerFactory>(),
            sp.GetRequiredService<UrStartupOptions>(),
            userDataDirectory));

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
            ".ur");

    internal static string DefaultUserSettingsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "settings.json");
}
