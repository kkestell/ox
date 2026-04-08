using System.Runtime.InteropServices;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Ur.Configuration;
using Ur.Configuration.Keyring;
using Ur.Extensions;
using Ur.Logging;
using Ur.Providers;
using Ur.Sessions;
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
/// <see cref="UrOptions"/> is bound to the "ur" section for strongly-typed access;
/// extension settings use <see cref="IConfiguration"/> directly with string keys.
/// </summary>
public static class ServiceCollectionExtensions
{
    public static IServiceCollection AddUr(
        this IServiceCollection services,
        UrStartupOptions options)
    {
        // Custom file logger writes to ~/.ur/logs/ur-{date}.log in the same format
        // as the former static UrLogger. Registered before services so any factory
        // that logs during construction will have a provider available.
        services.AddLogging(builder => builder.AddProvider(new UrFileLoggerProvider()));

        // Store options so other registrations and UrHost can access overrides.
        services.AddSingleton(options);

        services.AddSingleton(sp =>
        {
            var w = new Workspace(options.WorkspacePath);
            w.EnsureDirectories();
            return w;
        });

        services.AddSingleton<IKeyring>(sp =>
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
        {
            var cacheDir = Path.Combine(userDataDirectory, "cache");
            var logger = sp.GetRequiredService<ILoggerFactory>().CreateLogger(nameof(ModelCatalog));
            var catalog = new ModelCatalog(cacheDir, logger);
            _ = catalog.LoadCache();
            return catalog;
        });

        services.AddSingleton(sp =>
            new SessionStore(
                sp.GetRequiredService<Workspace>().SessionsDirectory,
                sp.GetRequiredService<ILoggerFactory>().CreateLogger<SessionStore>()));

        // Extension discovery produces the raw extension list. Schema registry and
        // catalog both depend on it, so it's registered as its own singleton.
        services.AddSingleton(sp =>
        {
            var workspace = sp.GetRequiredService<Workspace>();
            var logger = sp.GetRequiredService<ILoggerFactory>().CreateLogger(nameof(ExtensionLoader));
            return ExtensionLoader.DiscoverAll(
                options.SystemExtensionsPath ?? DefaultSystemExtensionsPath(userDataDirectory),
                options.UserExtensionsPath ?? DefaultUserExtensionsPath(userDataDirectory),
                workspace.ExtensionsDirectory,
                logger);
        });

        services.AddSingleton(sp =>
        {
            var registry = new SettingsSchemaRegistry();
            RegisterCoreSchemas(registry);
            var logger = sp.GetRequiredService<ILoggerFactory>().CreateLogger(nameof(ServiceCollectionExtensions));
            RegisterExtensionSchemas(registry, sp.GetRequiredService<List<Extension>>(), logger);
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
            var loggerFactory = sp.GetRequiredService<ILoggerFactory>();
            var overrideLogger = loggerFactory.CreateLogger(nameof(ExtensionOverrideStore));
            var overrideStore = new ExtensionOverrideStore(userDataDirectory, workspace, overrideLogger);
            var catalogLogger = loggerFactory.CreateLogger(nameof(ExtensionCatalog));
            return ExtensionCatalog.Create(
                sp.GetRequiredService<List<Extension>>(), overrideStore, catalogLogger);
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
        // Registered after SettingsWriter and IKeyring (which providers depend on) and
        // before UrConfiguration (which delegates readiness checks to the selected provider).
        services.AddSingleton(sp =>
        {
            var keyring = sp.GetRequiredService<IKeyring>();
            var settingsWriter = sp.GetRequiredService<SettingsWriter>();
            var registry = new ProviderRegistry();
            registry.Register(new OpenRouterProvider(keyring));
            registry.Register(new OpenAiProvider(keyring));
            registry.Register(new GoogleProvider(keyring));
            registry.Register(new OllamaProvider(settingsWriter));
            return registry;
        });

        services.AddSingleton(sp => new UrConfiguration(
            sp.GetRequiredService<ModelCatalog>(),
            sp.GetRequiredService<IOptionsMonitor<UrOptions>>(),
            sp.GetRequiredService<SettingsWriter>(),
            sp.GetRequiredService<IKeyring>(),
            sp.GetRequiredService<ProviderRegistry>()));

        // UrHost: registered via factory because the constructor is internal
        // (UrHost is a public type but we don't want arbitrary external construction).
        services.AddSingleton(sp => new UrHost(
            sp.GetRequiredService<Workspace>(),
            sp.GetRequiredService<SessionStore>(),
            sp.GetRequiredService<ExtensionCatalog>(),
            sp.GetRequiredService<SkillRegistry>(),
            sp.GetRequiredService<BuiltInCommandRegistry>(),
            sp.GetRequiredService<SettingsSchemaRegistry>(),
            sp.GetRequiredService<UrConfiguration>(),
            sp.GetRequiredService<ProviderRegistry>(),
            sp.GetRequiredService<ILoggerFactory>(),
            sp.GetRequiredService<UrStartupOptions>()));

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
        registry.Register(OllamaProvider.UriSettingKey, stringSchema);
    }

    /// <summary>
    /// Registers extension settings schemas, skipping extensions whose schemas
    /// conflict with already-registered keys.
    /// </summary>
    internal static void RegisterExtensionSchemas(
        SettingsSchemaRegistry registry,
        IEnumerable<Extension> discoveredExtensions,
        ILogger? logger = null)
    {
        foreach (var extension in discoveredExtensions)
        {
            try
            {
                var duplicateKey = extension.SettingsSchemas.Keys
                    .FirstOrDefault(registry.IsKnown);
                if (duplicateKey is not null)
                {
                    throw new InvalidOperationException(
                        $"Settings key '{duplicateKey}' is already registered.");
                }

                foreach (var (key, schema) in extension.SettingsSchemas)
                    registry.Register(key, schema);
            }
            catch (InvalidOperationException ex)
            {
                logger?.LogWarning(
                    "Extension '{ExtensionName}' skipped: failed to register settings schemas: {Error}",
                    extension.Name, ex.Message);
            }
        }
    }

    internal static string DefaultUserDataDirectory() =>
        Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".ur");

    internal static string DefaultSystemExtensionsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "extensions", "system");

    internal static string DefaultUserExtensionsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "extensions", "user");

    internal static string DefaultUserSettingsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "settings.json");
}
