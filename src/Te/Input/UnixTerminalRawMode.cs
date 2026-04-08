using System.Runtime.InteropServices;

namespace Te.Input;

/// <summary>
/// Minimal raw-mode bridge for Unix-like terminals.
/// Te only needs enough terminal control to read raw bytes and parse SGR mouse
/// sequences. The broader ConsoleEx terminal-management surface is intentionally
/// left behind so this extraction remains small.
/// </summary>
internal static class UnixTerminalRawMode
{
    private const int StandardInputFileDescriptor = 0;
    private const int ApplyNow = 0;

    private const uint EchoLinux = 0x00000008;
    private const uint CanonicalLinux = 0x00000002;
    private const uint SignalsLinux = 0x00000001;
    private const uint ExtendedLinux = 0x00008000;
    private const uint OutputPostProcessLinux = 0x00000001;
    private const uint InputCrToNlLinux = 0x00000100;
    private const uint InputNlToCrLinux = 0x00000040;
    private const uint IgnoreCrLinux = 0x00000080;
    private const uint BreakInterruptLinux = 0x00000002;
    private const uint StripLinux = 0x00000020;
    private const uint MarkParityLinux = 0x00000008;
    private const uint FlowControlLinux = 0x00000400;

    private const ulong EchoMac = 0x00000008;
    private const ulong CanonicalMac = 0x00000100;
    private const ulong SignalsMac = 0x00000080;
    private const ulong ExtendedMac = 0x00000400;
    private const ulong OutputPostProcessMac = 0x00000001;
    private const ulong InputCrToNlMac = 0x00000100;
    private const ulong InputNlToCrMac = 0x00000040;
    private const ulong IgnoreCrMac = 0x00000080;
    private const ulong BreakInterruptMac = 0x00000002;
    private const ulong StripMac = 0x00000020;
    private const ulong MarkParityMac = 0x00000008;
    private const ulong FlowControlMac = 0x00000200;

    private const byte LinuxVTimeIndex = 5;
    private const byte LinuxVMinIndex = 6;
    private const byte MacVMinIndex = 16;
    private const byte MacVTimeIndex = 17;
    private const short PollIn = 0x0001;

    private static bool _isEnabled;
    private static LinuxTermios _savedLinuxTermios;
    private static MacTermios _savedMacTermios;

    public static bool IsSupported => OperatingSystem.IsLinux() || OperatingSystem.IsMacOS();

    public static void Enable()
    {
        if (!IsSupported || _isEnabled)
            return;

        if (OperatingSystem.IsLinux())
        {
            if (tcgetattr_linux(StandardInputFileDescriptor, out _savedLinuxTermios) != 0)
                throw new InvalidOperationException("Unable to read current terminal mode.");

            var raw = _savedLinuxTermios;
            raw.c_iflag &= ~(BreakInterruptLinux | InputCrToNlLinux | InputNlToCrLinux | IgnoreCrLinux | StripLinux | FlowControlLinux | MarkParityLinux);
            raw.c_oflag &= ~OutputPostProcessLinux;
            raw.c_lflag &= ~(EchoLinux | CanonicalLinux | SignalsLinux | ExtendedLinux);
            raw.c_cc[LinuxVTimeIndex] = 0;
            raw.c_cc[LinuxVMinIndex] = 1;

            if (tcsetattr_linux(StandardInputFileDescriptor, ApplyNow, ref raw) != 0)
                throw new InvalidOperationException("Unable to switch terminal to raw mode.");
        }
        else
        {
            if (tcgetattr_mac(StandardInputFileDescriptor, out _savedMacTermios) != 0)
                throw new InvalidOperationException("Unable to read current terminal mode.");

            var raw = _savedMacTermios;
            raw.c_iflag &= ~(BreakInterruptMac | InputCrToNlMac | InputNlToCrMac | IgnoreCrMac | StripMac | FlowControlMac | MarkParityMac);
            raw.c_oflag &= ~OutputPostProcessMac;
            raw.c_lflag &= ~(EchoMac | CanonicalMac | SignalsMac | ExtendedMac);
            raw.c_cc[MacVTimeIndex] = 0;
            raw.c_cc[MacVMinIndex] = 1;

            if (tcsetattr_mac(StandardInputFileDescriptor, ApplyNow, ref raw) != 0)
                throw new InvalidOperationException("Unable to switch terminal to raw mode.");
        }

        _isEnabled = true;
    }

    public static void Disable()
    {
        if (!_isEnabled)
            return;

        try
        {
            if (OperatingSystem.IsLinux())
                _ = tcsetattr_linux(StandardInputFileDescriptor, ApplyNow, ref _savedLinuxTermios);
            else if (OperatingSystem.IsMacOS())
                _ = tcsetattr_mac(StandardInputFileDescriptor, ApplyNow, ref _savedMacTermios);
        }
        finally
        {
            _isEnabled = false;
        }
    }

    public static int ReadByteWithTimeout(int timeoutMs)
    {
        var descriptor = new PollDescriptor
        {
            FileDescriptor = StandardInputFileDescriptor,
            Events = PollIn,
        };

        if (poll(ref descriptor, 1, timeoutMs) <= 0 || (descriptor.ReturnedEvents & PollIn) == 0)
            return -1;

        var buffer = new byte[1];
        return read(StandardInputFileDescriptor, buffer, 1) == 1 ? buffer[0] : -1;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct LinuxTermios
    {
        public uint c_iflag;
        public uint c_oflag;
        public uint c_cflag;
        public uint c_lflag;
        public byte c_line;
        [MarshalAs(UnmanagedType.ByValArray, SizeConst = 32)]
        public byte[] c_cc;
        public uint c_ispeed;
        public uint c_ospeed;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct MacTermios
    {
        public ulong c_iflag;
        public ulong c_oflag;
        public ulong c_cflag;
        public ulong c_lflag;
        [MarshalAs(UnmanagedType.ByValArray, SizeConst = 20)]
        public byte[] c_cc;
        public ulong c_ispeed;
        public ulong c_ospeed;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct PollDescriptor
    {
        public int FileDescriptor;
        public short Events;
        public short ReturnedEvents;
    }

    [DllImport("libc", SetLastError = true, EntryPoint = "tcgetattr")]
    private static extern int tcgetattr_linux(int fd, out LinuxTermios termios);

    [DllImport("libc", SetLastError = true, EntryPoint = "tcsetattr")]
    private static extern int tcsetattr_linux(int fd, int optionalActions, ref LinuxTermios termios);

    [DllImport("libc", SetLastError = true, EntryPoint = "tcgetattr")]
    private static extern int tcgetattr_mac(int fd, out MacTermios termios);

    [DllImport("libc", SetLastError = true, EntryPoint = "tcsetattr")]
    private static extern int tcsetattr_mac(int fd, int optionalActions, ref MacTermios termios);

    [DllImport("libc", SetLastError = true)]
    private static extern int poll(ref PollDescriptor descriptors, int count, int timeoutMs);

    [DllImport("libc", SetLastError = true)]
    private static extern int read(int fd, [Out] byte[] buffer, int count);
}
