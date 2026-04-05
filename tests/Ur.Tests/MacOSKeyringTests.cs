using Ur.Configuration.Keyring;
using System.Runtime.InteropServices;

namespace Ur.Tests;

public class MacOSKeyringTests
{
    [Fact]
    public void RoundTrip_StoreAndRetrieve()
    {
        if (!RuntimeInformation.IsOSPlatform(OSPlatform.OSX))
            return;

        var keyring = new MacOSKeyring();
        var service = "ur-test";
        var account = "spike-roundtrip";
        var secret = $"test-secret-{Guid.NewGuid()}";

        try
        {
            keyring.SetSecret(service, account, secret);
            var retrieved = keyring.GetSecret(service, account);
            Assert.Equal(secret, retrieved);
        }
        finally
        {
            keyring.DeleteSecret(service, account);
        }
    }

    [Fact]
    public void GetSecret_NotFound_ReturnsNull()
    {
        if (!RuntimeInformation.IsOSPlatform(OSPlatform.OSX))
            return;

        var result = new MacOSKeyring().GetSecret("ur-test", "nonexistent-account");
        Assert.Null(result);
    }

    [Fact]
    public void DeleteSecret_ThenLookup_ReturnsNull()
    {
        if (!RuntimeInformation.IsOSPlatform(OSPlatform.OSX))
            return;

        var keyring = new MacOSKeyring();
        var service = "ur-test";
        var account = "spike-delete";

        keyring.SetSecret(service, account, "to-be-deleted");
        keyring.DeleteSecret(service, account);

        var result = keyring.GetSecret(service, account);
        Assert.Null(result);
    }

    [Fact]
    public void SetSecret_Overwrite_ReturnsLatest()
    {
        if (!RuntimeInformation.IsOSPlatform(OSPlatform.OSX))
            return;

        var keyring = new MacOSKeyring();
        var service = "ur-test";
        var account = "spike-overwrite";

        try
        {
            keyring.SetSecret(service, account, "first");
            keyring.SetSecret(service, account, "second");
            Assert.Equal("second", keyring.GetSecret(service, account));
        }
        finally
        {
            keyring.DeleteSecret(service, account);
        }
    }
}
