using System.Runtime.InteropServices;

namespace Ur.Configuration.Keyring;

/// <summary>
/// Linux keyring implementation using libsecret's "v" (non-varargs) API via LibraryImport.
/// Uses GHashTable for attribute passing, which has a fixed function signature compatible
/// with .NET source-generated P/Invoke.
/// </summary>
public sealed partial class LinuxKeyring : IKeyring
{
    private const string LibSecret = "libsecret-1.so.0";
    private const string LibGLib = "libglib-2.0.so.0";

    // SecretSchemaFlags
    private const int SecretSchemaNone = 0;

    // SecretSchemaAttributeType
    private const int SecretSchemaAttributeString = 0;

    #region LibSecret P/Invoke

    [LibraryImport(LibSecret, StringMarshalling = StringMarshalling.Utf8)]
    private static partial nint secret_password_lookupv_sync(
        ref SecretSchemaManaged schema,
        nint attributes,
        nint cancellable,
        out nint error);

    [LibraryImport(LibSecret, StringMarshalling = StringMarshalling.Utf8)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static partial bool secret_password_storev_sync(
        ref SecretSchemaManaged schema,
        nint attributes,
        string? collection,
        string label,
        string password,
        nint cancellable,
        out nint error);

    [LibraryImport(LibSecret, StringMarshalling = StringMarshalling.Utf8)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static partial bool secret_password_clearv_sync(
        ref SecretSchemaManaged schema,
        nint attributes,
        nint cancellable,
        out nint error);

    [LibraryImport(LibSecret)]
    private static partial void secret_password_free(nint password);

    #endregion

    #region GLib P/Invoke

    // g_hash_table_new_full so we can set key/value destroy functions (g_free)
    // to properly free the g_strdup'd strings when the table is destroyed.
    [LibraryImport(LibGLib)]
    private static partial nint g_hash_table_new_full(
        nint hashFunc, nint equalFunc,
        nint keyDestroyFunc, nint valueDestroyFunc);

    [LibraryImport(LibGLib)]
    private static partial void g_hash_table_insert(nint hashTable, nint key, nint value);

    [LibraryImport(LibGLib)]
    private static partial void g_hash_table_destroy(nint hashTable);

    [LibraryImport(LibGLib)]
    private static partial void g_error_free(nint error);

    [LibraryImport(LibGLib, StringMarshalling = StringMarshalling.Utf8)]
    private static partial nint g_strdup(string str);

    #endregion

    // Function pointer addresses resolved once at startup.
    private static readonly nint GStrHash;
    private static readonly nint GStrEqual;
    private static readonly nint GFree;

    static LinuxKeyring()
    {
        var lib = NativeLibrary.Load(LibGLib);
        GStrHash = NativeLibrary.GetExport(lib, "g_str_hash");
        GStrEqual = NativeLibrary.GetExport(lib, "g_str_equal");
        GFree = NativeLibrary.GetExport(lib, "g_free");
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct SecretSchemaAttribute
    {
        public nint Name; // const gchar*
        public int Type;  // SecretSchemaAttributeType
    }

    // Fixed-size schema struct with 3 attribute slots (service, account, sentinel).
    // Padded to match the full C struct size (592 bytes on x64).
    //
    // C layout: name(8) + flags(4) + pad(4) + 32*attr(32*16) + reserved(8*8)
    //         = 16 + 512 + 64 = 592
    // Our fields: name(8) + flags(4) + pad(4) + 3*attr(48) = 64
    // Remaining padding: 528
    [StructLayout(LayoutKind.Sequential)]
    private struct SecretSchemaManaged
    {
        public nint Name;
        public int Flags;
        public SecretSchemaAttribute Attr0;
        public SecretSchemaAttribute Attr1;
        public SecretSchemaAttribute Attr2; // sentinel: Name = 0
        private unsafe fixed byte _padding[528];
    }

    // Leaked intentionally — these live for the process lifetime.
    private static readonly nint SchemaName = Marshal.StringToHGlobalAnsi("ur.keyring");
    private static readonly nint AttrServiceName = Marshal.StringToHGlobalAnsi("service");
    private static readonly nint AttrAccountName = Marshal.StringToHGlobalAnsi("account");

    private static SecretSchemaManaged CreateSchema()
    {
        return new SecretSchemaManaged
        {
            Name = SchemaName,
            Flags = SecretSchemaNone,
            Attr0 = new SecretSchemaAttribute { Name = AttrServiceName, Type = SecretSchemaAttributeString },
            Attr1 = new SecretSchemaAttribute { Name = AttrAccountName, Type = SecretSchemaAttributeString },
            Attr2 = new SecretSchemaAttribute { Name = 0, Type = 0 }
        };
    }

    /// <summary>
    /// Creates a GHashTable with g_strdup'd keys and values. The table owns the strings
    /// and will g_free them on destroy (via g_hash_table_new_full).
    /// </summary>
    private static nint CreateAttributeTable(string service, string account)
    {
        var table = g_hash_table_new_full(GStrHash, GStrEqual, GFree, GFree);
        g_hash_table_insert(table, g_strdup("service"), g_strdup(service));
        g_hash_table_insert(table, g_strdup("account"), g_strdup(account));
        return table;
    }

    private static void ThrowIfError(nint error)
    {
        if (error == 0) return;

        // GError: { guint32 domain; gint code; gchar* message; }
        var message = Marshal.PtrToStringUTF8(Marshal.ReadIntPtr(error, 2 * sizeof(int)));
        g_error_free(error);
        throw new InvalidOperationException($"libsecret error: {message}");
    }

    public string? GetSecret(string service, string account)
    {
        var schema = CreateSchema();
        var attrs = CreateAttributeTable(service, account);
        try
        {
            var result = secret_password_lookupv_sync(ref schema, attrs, 0, out var error);
            ThrowIfError(error);

            if (result == 0)
                return null;

            var secret = Marshal.PtrToStringUTF8(result);
            secret_password_free(result);
            return secret;
        }
        finally
        {
            g_hash_table_destroy(attrs);
        }
    }

    public void SetSecret(string service, string account, string secret)
    {
        var schema = CreateSchema();
        var attrs = CreateAttributeTable(service, account);
        try
        {
            var ok = secret_password_storev_sync(
                ref schema, attrs, null, $"ur:{service}/{account}", secret, 0, out var error);
            ThrowIfError(error);

            if (!ok)
                throw new InvalidOperationException("secret_password_storev_sync returned false");
        }
        finally
        {
            g_hash_table_destroy(attrs);
        }
    }

    public void DeleteSecret(string service, string account)
    {
        var schema = CreateSchema();
        var attrs = CreateAttributeTable(service, account);
        try
        {
            _ = secret_password_clearv_sync(ref schema, attrs, 0, out var error);
            ThrowIfError(error);
        }
        finally
        {
            g_hash_table_destroy(attrs);
        }
    }
}
