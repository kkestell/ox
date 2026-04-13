using System.Runtime.CompilerServices;

// Allow the unit and integration test projects to test internal types
// without making them public API.
[assembly: InternalsVisibleTo("Ox.Tests")]
[assembly: InternalsVisibleTo("Ox.IntegrationTests")]
