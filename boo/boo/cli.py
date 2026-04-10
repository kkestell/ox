"""CLI entry point for the boo session daemon.

Provides subcommands that either start the daemon or communicate with it
via the Unix domain socket.  Each client subcommand connects, sends one
JSON command, prints the result, and exits.

Usage:
    boo start "ur-tui"          # Launch a persistent session
    boo screen                  # Print current screen contents
    boo type "hello world"      # Type text into the session
    boo press enter             # Press a key
    boo stop                    # Tear down the session
    boo alive                   # Check if the child process is alive
"""

from __future__ import annotations

import argparse
import os
import select
import subprocess
import sys
import time

from boo.client import DEFAULT_SOCKET_PATH, DaemonNotRunningError, send_command


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(
        prog="boo",
        description="Drive interactive TUI applications through a persistent session daemon.",
    )
    parser.add_argument(
        "--socket",
        default=DEFAULT_SOCKET_PATH,
        help=f"Path to the daemon socket (default: {DEFAULT_SOCKET_PATH})",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    # -- boo start <cmd> ---------------------------------------------------
    start_parser = subparsers.add_parser("start", help="Start a persistent session")
    start_parser.add_argument("cmd", help="Shell command to run in the session")
    start_parser.add_argument("--cols", type=int, default=80, help="Terminal columns")
    start_parser.add_argument("--rows", type=int, default=24, help="Terminal rows")

    # -- boo screen --------------------------------------------------------
    screen_parser = subparsers.add_parser("screen", help="Print the current screen")
    screen_parser.add_argument(
        "--no-trim", action="store_true", default=False,
        help="Don't trim trailing whitespace from lines",
    )
    screen_parser.add_argument(
        "--unwrap", action="store_true", default=False,
        help="Unwrap soft-wrapped lines",
    )

    # -- boo type <text> ---------------------------------------------------
    type_parser = subparsers.add_parser("type", help="Type text into the session")
    type_parser.add_argument("text", help="Text to type")

    # -- boo press <key> ---------------------------------------------------
    press_parser = subparsers.add_parser("press", help="Press a key")
    press_parser.add_argument("key", help="Key name (e.g. enter, tab, escape, a, f1)")
    press_parser.add_argument("--ctrl", action="store_true", default=False)
    press_parser.add_argument("--alt", action="store_true", default=False)
    press_parser.add_argument("--shift", action="store_true", default=False)

    # -- boo stop ----------------------------------------------------------
    subparsers.add_parser("stop", help="Stop the daemon and clean up")

    # -- boo alive ---------------------------------------------------------
    subparsers.add_parser("alive", help="Check if the child process is alive")

    args = parser.parse_args(argv)

    try:
        if args.command == "start":
            _cmd_start(args)
        elif args.command == "screen":
            _cmd_screen(args)
        elif args.command == "type":
            _cmd_type(args)
        elif args.command == "press":
            _cmd_press(args)
        elif args.command == "stop":
            _cmd_stop(args)
        elif args.command == "alive":
            _cmd_alive(args)
    except DaemonNotRunningError as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
    except Exception as exc:
        print(f"Error: {exc}", file=sys.stderr)
        sys.exit(1)


# ---------------------------------------------------------------------------
# Subcommand handlers
# ---------------------------------------------------------------------------

def _cmd_start(args: argparse.Namespace) -> None:
    """Fork the daemon into the background, wait for it to be ready."""
    # Build the command that will run the daemon in a subprocess.  We invoke
    # the daemon module directly so we don't depend on the CLI being installed
    # yet (editable installs, development, etc.).
    daemon_cmd = [
        sys.executable, "-m", "boo.daemon",
        "--command", args.cmd,
        "--socket", args.socket,
        "--cols", str(args.cols),
        "--rows", str(args.rows),
    ]

    # Launch the daemon as a detached subprocess.  stdout is piped so we
    # can wait for the "ready" signal; stderr goes to our stderr for
    # debugging.
    proc = subprocess.Popen(
        daemon_cmd,
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        # Start a new process group so the daemon isn't killed when the
        # calling terminal exits.
        start_new_session=True,
    )

    # Wait for the daemon to signal readiness (it writes "ready\n" to
    # stdout once the socket is listening and the session is launched).
    # We use select() to avoid blocking on readline() — the daemon may
    # emit unexpected startup output before "ready", and a blocking read
    # would prevent us from detecting a dead process.
    deadline = time.monotonic() + 10.0
    while time.monotonic() < deadline:
        # Check if the daemon died during startup.
        if proc.poll() is not None:
            print(
                f"Daemon exited during startup (exit code {proc.returncode})",
                file=sys.stderr,
            )
            sys.exit(1)

        # Use select() with a short timeout to check if stdout has data
        # before reading — this keeps the loop non-blocking so we can
        # detect process death on the next iteration.
        if proc.stdout:
            ready, _, _ = select.select([proc.stdout], [], [], 0.05)
            if ready:
                line = proc.stdout.readline()
                if line.strip() == b"ready":
                    print(f"Session started (PID {proc.pid}, socket {args.socket})")
                    return
                # Otherwise it's an unexpected line — keep looping to wait
                # for the actual "ready" signal.
        else:
            time.sleep(0.05)

    print("Timed out waiting for daemon to become ready", file=sys.stderr)
    proc.kill()
    sys.exit(1)


def _cmd_screen(args: argparse.Namespace) -> None:
    """Fetch and print the current screen contents."""
    response = send_command(
        {"cmd": "screen", "trim": not args.no_trim, "unwrap": args.unwrap},
        socket_path=args.socket,
    )
    # Print the screen text to stdout — no trailing newline since the
    # screen text already includes line structure.
    sys.stdout.write(response["text"])
    # Ensure there's a final newline so the shell prompt lands cleanly.
    if not response["text"].endswith("\n"):
        sys.stdout.write("\n")


def _cmd_type(args: argparse.Namespace) -> None:
    send_command({"cmd": "type", "text": args.text}, socket_path=args.socket)


def _cmd_press(args: argparse.Namespace) -> None:
    send_command(
        {
            "cmd": "press",
            "key": args.key,
            "ctrl": args.ctrl,
            "alt": args.alt,
            "shift": args.shift,
        },
        socket_path=args.socket,
    )


def _cmd_stop(args: argparse.Namespace) -> None:
    send_command({"cmd": "stop"}, socket_path=args.socket)
    print("Session stopped")


def _cmd_alive(args: argparse.Namespace) -> None:
    response = send_command({"cmd": "alive"}, socket_path=args.socket)
    alive = response.get("alive", False)
    print("alive" if alive else "not alive")
    if not alive:
        sys.exit(1)


if __name__ == "__main__":
    main()
