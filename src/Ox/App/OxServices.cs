using System.Runtime.InteropServices;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Ox.Agent;
using Ox.Agent.Compaction;
using Ox.Agent.Configuration;
using Ox.Agent.Configuration.Keyring;
using Ox.Agent.Hosting;
using Ox.Agent.Permissions;
using Ox.Agent.Providers;
using Ox.Agent.Sessions;
using Ox.Agent.Settings;
using Ox.Agent.Skills;
using Ox.Agent.Tools;
using Ox.App.Configuration;

namespace Ox.App;

/// <summary>
/// Single internal entry point for wiring up the full Ox service graph.
///
/// This is App-layer code: it orchestrates bootstrap by calling into Agent-layer
/// types (Workspace, OxConfiguration, OxHost, etc.) and wiring up App-specific
/// services (ProviderRegistration, ModelCatalog). Placing it in Agent/ would
/// violate the layer rule because it references App-layer types like
/// <see cref="ModelCatalog"/> and <see cref="ProviderConfig"/>.
///
/// Program.cs calls <see cref="AddSettingsSources"/> during configuration setup
/// and <see cref="Register"/> during service registration.
/// </summary>
internal static class OxServices
{
    /// <summary>
    /// Registers the user-level and workspace-level settings files as configuration
    /// sources. Call this on the host's <see cref="IConfigurationBuilder"/> before
    /// <see cref="Register"/> so that <see cref="IConfigurationRoot.Reload"/>
    /// propagates changes through the standard options pipeline.
    ///
    /// Two <see cref="OxSettingsConfigurationSource"/> instances are added: user file
    /// first, workspace file second. IConfiguration's "last source wins" rule means
    /// workspace values override user values for the same key.
    /// </summary>
    internal static void AddSettingsSources(
        IConfigurationBuilder builder,
        string userSettingsPath,
        string workspaceSettingsPath)
    {
        builder.Add(new OxSettingsConfigurationSource(userSettingsPath));
        builder.Add(new OxSettingsConfigurationSource(workspaceSettingsPath));
    }

    /// <summary>
    /// Registers the entire Ox service graph into the DI container.
    ///
    /// Combines what was previously split across AddUr (Agent-layer registration),
    /// AddProvidersFromConfig (App-layer provider dispatch), and scattered
    /// Program.cs registrations. A single method because there is only one consumer.
    ///
    /// The optional <paramref name="configure"/> callback sets runtime values
    /// (WorkspacePath, SelectedModelOverride, etc.) after file-based settings
    /// are bound from IConfiguration. Code overrides win over file values.
    ///
    /// To override the keyring, register <c>IKeyring</c> before calling this method —
    /// <c>TryAddSingleton</c> ensures pre-registered keyrings win.
    /// </summary>
    internal static void Register(
        IServiceCollection services,
        IConfiguration configuration,
        Action<OxOptions>? configure = null)
    {
        // ── Options pipeline ────────────────────────────────────────────
        // Bind the "ox" section to OxOptions. ConfigurationBinder.Bind matches
        // property names case-insensitively: Model ← "ox:model", etc.
        // Defaults (e.g. TurnsToKeepToolResults = 3) are preserved when keys are absent.
        services.Configure<OxOptions>(configuration.GetSection("ox"));

        if (configure is not null)
            services.PostConfigure(configure);

        // Ensure IConfigurationRoot is available in DI for SettingsWriter.Reload().
        if (configuration is IConfigurationRoot configRoot)
            services.AddSingleton(configRoot);

        // Snapshot options for values needed at registration time (paths).
        // PostConfigure callbacks haven't run yet, so we apply them manually.
        var snapshot = new OxOptions();
        configuration.GetSection("ox").Bind(snapshot);
        configure?.Invoke(snapshot);

        var userDataDirectory = snapshot.UserDataDirectory ?? DefaultUserDataDirectory();
        var userSettingsPath = snapshot.UserSettingsPath ?? DefaultUserSettingsPath(userDataDirectory);

        // ── Agent-layer services ────────────────────────────────────────

        services.AddSingleton(_ =>
        {
            var w = new Workspace(snapshot.WorkspacePath);
            w.EnsureDirectories();
            return w;
        });

        // TryAddSingleton lets callers pre-register a custom IKeyring (e.g. test
        // keyrings, EnvironmentKeyring for headless) before calling Register.
        services.TryAddSingleton<IKeyring>(_ => CreatePlatformKeyring());

        services.AddSingleton<ISessionStore>(sp =>
            new JsonlSessionStore(
                sp.GetRequiredService<Workspace>().SessionsDirectory,
                sp.GetRequiredService<ILoggerFactory>().CreateLogger<JsonlSessionStore>()));

        services.AddSingleton<ICompactionStrategy, Autocompactor>();

        services.AddSingleton(_ =>
        {
            var registry = new SettingsSchemaRegistry();
            RegisterCoreSchemas(registry);
            return registry;
        });

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

        services.AddSingleton<BuiltInCommandRegistry>();
        services.AddSingleton(sp => new CommandRegistry(
            sp.GetRequiredService<BuiltInCommandRegistry>(),
            sp.GetRequiredService<SkillRegistry>()));

        // Provider registry — collects all IProvider instances from DI and maps
        // them by name prefix.
        services.AddSingleton(sp =>
        {
            var registry = new ProviderRegistry();
            foreach (var provider in sp.GetServices<IProvider>())
                registry.Register(provider);
            return registry;
        });

        services.AddSingleton(sp => new OxConfiguration(
            sp.GetRequiredService<IOptionsMonitor<OxOptions>>(),
            sp.GetRequiredService<SettingsWriter>(),
            sp.GetRequiredService<IKeyring>(),
            sp.GetRequiredService<ProviderRegistry>()));

        // SessionDependencies bundles everything an OxSession needs. Building
        // it once in DI means OxHost's constructor can shrink to the handful of
        // values it actually owns (workspace, user-data paths, schema registry).
        services.AddSingleton(sp =>
        {
            var providerRegistry = sp.GetRequiredService<ProviderRegistry>();
            var workspace = sp.GetRequiredService<Workspace>();
            var loggerFactory = sp.GetRequiredService<ILoggerFactory>();

            // Permission grants are shared across every session in this host —
            // the store persists workspace + always grants to disk, so it
            // deliberately lives at host scope, not session scope.
            var grantStore = new PermissionGrantStore(
                workspace.PermissionsPath,
                OxHost.DefaultUserPermissionsPath(userDataDirectory),
                loggerFactory.CreateLogger<PermissionGrantStore>());

            // Fold the optional Func<string, IChatClient> override into the
            // factory here (S5). OxHost never sees the override as a separate
            // thing — it just gets a single chat-client factory.
            var chatClientFactory = sp.GetService<Func<string, IChatClient>>()
                ?? providerRegistry.CreateChatClient;

            return new SessionDependencies(
                Configuration: sp.GetRequiredService<OxConfiguration>(),
                Skills: sp.GetRequiredService<SkillRegistry>(),
                BuiltInCommands: sp.GetRequiredService<BuiltInCommandRegistry>(),
                Workspace: workspace,
                LoggerFactory: loggerFactory,
                Sessions: sp.GetRequiredService<ISessionStore>(),
                CompactionStrategy: sp.GetRequiredService<ICompactionStrategy>(),
                ChatClientFactory: chatClientFactory,
                ConfigureChatOptions: providerRegistry.ConfigureChatOptions,
                ResolveContextWindow: sp.GetService<Func<string, int?>>() ?? (_ => null),
                AdditionalTools: sp.GetService<ToolRegistry>(),
                GrantStore: grantStore);
        });

        services.AddSingleton(sp => new OxHost(
            sp.GetRequiredService<SessionDependencies>(),
            sp.GetRequiredService<Workspace>(),
            sp.GetRequiredService<ILoggerFactory>(),
            sp.GetRequiredService<SettingsSchemaRegistry>()));

        // ── App-layer services ──────────────────────────────────────────

        services.AddSingleton<ModelCatalog>();
        services.AddSingleton<Func<string, int?>>(sp =>
            sp.GetRequiredService<ModelCatalog>().ResolveContextWindow);
    }

    // --- Internal helpers ---

    internal static IKeyring CreatePlatformKeyring()
    {
        if (RuntimeInformation.IsOSPlatform(OSPlatform.OSX))
            return new MacOsKeyring();
        return RuntimeInformation.IsOSPlatform(OSPlatform.Linux)
            ? new LinuxKeyring()
            : throw new PlatformNotSupportedException("Ox requires macOS or Linux.");
    }

    internal static void RegisterCoreSchemas(SettingsSchemaRegistry registry)
    {
        var stringSchema = System.Text.Json.JsonDocument.Parse("""{"type":"string"}""")
            .RootElement.Clone();
        registry.Register(OxConfiguration.ModelSettingKey, stringSchema);
    }

    internal static string DefaultUserDataDirectory() =>
        Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".ox");

    internal static string DefaultUserSettingsPath(string userDataDirectory) =>
        Path.Combine(userDataDirectory, "settings.json");
}
