namespace Ur.Configuration.Keyring;

public interface IKeyring
{
    string? GetSecret(string service, string account);
    void SetSecret(string service, string account, string secret);
    void DeleteSecret(string service, string account);
}
