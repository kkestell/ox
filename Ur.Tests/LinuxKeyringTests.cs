using Ur.Configuration.Keyring;

namespace Ur.Tests;

public class LinuxKeyringTests
{
    private readonly LinuxKeyring _keyring = new();

    [Fact]
    public void RoundTrip_StoreAndRetrieve()
    {
        var service = "ur-test";
        var account = "spike-roundtrip";
        var secret = $"test-secret-{Guid.NewGuid()}";

        try
        {
            _keyring.SetSecret(service, account, secret);
            var retrieved = _keyring.GetSecret(service, account);
            Assert.Equal(secret, retrieved);
        }
        finally
        {
            _keyring.DeleteSecret(service, account);
        }
    }

    [Fact]
    public void GetSecret_NotFound_ReturnsNull()
    {
        var result = _keyring.GetSecret("ur-test", "nonexistent-account");
        Assert.Null(result);
    }

    [Fact]
    public void DeleteSecret_ThenLookup_ReturnsNull()
    {
        var service = "ur-test";
        var account = "spike-delete";

        _keyring.SetSecret(service, account, "to-be-deleted");
        _keyring.DeleteSecret(service, account);

        var result = _keyring.GetSecret(service, account);
        Assert.Null(result);
    }

    [Fact]
    public void SetSecret_Overwrite_ReturnsLatest()
    {
        var service = "ur-test";
        var account = "spike-overwrite";

        try
        {
            _keyring.SetSecret(service, account, "first");
            _keyring.SetSecret(service, account, "second");
            Assert.Equal("second", _keyring.GetSecret(service, account));
        }
        finally
        {
            _keyring.DeleteSecret(service, account);
        }
    }
}
