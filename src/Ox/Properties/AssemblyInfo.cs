using System.Runtime.CompilerServices;

// Allow the unit test project to test internal types like HeadlessRunner
// and OxBootOptions without making them public API.
[assembly: InternalsVisibleTo("Ur.Tests")]
