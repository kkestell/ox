using EvalShared;

namespace EvalRunner;

/// <summary>
/// Builds the Ox CLI argument list for a single headless eval run.
///
/// Pulling this logic out of <see cref="ContainerRunner"/> keeps model-specific
/// argument selection testable without having to spawn Podman in unit tests.
/// </summary>
internal static class OxHeadlessArgsBuilder
{
    internal static IReadOnlyList<string> Build(string model, ScenarioDefinition scenario)
    {
        var args = new List<string>
        {
            "--headless",
            "--yolo",
            "--prompt",
            scenario.Prompt,
        };

        // Fake models use the same Ox testing path as Boo and unit tests. Wiring
        // them through the eval harness gives us a deterministic end-to-end smoke
        // scenario that doesn't consume provider API quotas.
        if (model.StartsWith("fake/", StringComparison.OrdinalIgnoreCase))
        {
            args.Insert(2, model["fake/".Length..]);
            args.Insert(2, "--fake-provider");
        }
        else
        {
            args.Insert(2, model);
            args.Insert(2, "--model");
        }

        if (scenario.MaxIterations.HasValue)
        {
            args.Add("--max-iterations");
            args.Add(scenario.MaxIterations.Value.ToString(System.Globalization.CultureInfo.InvariantCulture));
        }

        return args;
    }
}
