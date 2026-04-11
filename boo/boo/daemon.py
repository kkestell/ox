"""Boo session daemon — keeps a Session alive across CLI invocations.

Runs a single-threaded event loop that interleaves PTY pumping
(``session.step()``) with ``select()`` on a Unix domain socket. Each client
connection delivers one JSON command and receives one JSON response, then
disconnects.

The daemon owns the Session instance.  Clients never touch it directly —
they send commands over the socket and get JSON back.  This is the only
interface boundary.
"""

from __future__ import annotations

import atexit
import json
import os
import select
import signal
import socket
import sys
import traceback

from ._errors import BooError
from ._session import Session
from .client import DEFAULT_SOCKET_PATH


def run_daemon(
    command: str,
    *,
    socket_path: str = DEFAULT_SOCKET_PATH,
    cols: int = 80,
    rows: int = 24,
) -> None:
    """Launch a Boo session and serve commands over a Unix domain socket.

    This function blocks forever (or until a ``stop`` command arrives).
    It is designed to be called from a forked/backgrounded process so the
    CLI can return immediately after the socket is ready.

    The main loop:
      1. ``select()`` on the listening socket + any active client fd (~16ms timeout)
      2. Accept new connections
      3. Read/dispatch/respond to client commands (one per connection)
      4. ``step(0)`` to pump PTY I/O
      5. Repeat
    """
    # Clean up any stale socket from a previous run.
    _remove_stale_socket(socket_path)

    session = Session.launch(
        command,
        cols=cols,
        rows=rows,
    )

    server_sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server_sock.bind(socket_path)
    server_sock.listen(1)
    server_sock.setblocking(False)

    pid_path = _pid_path_for(socket_path)
    _write_pid_file(pid_path)

    # Ensure cleanup on exit regardless of how we leave.
    def cleanup():
        _cleanup(session, server_sock, socket_path, pid_path)

    atexit.register(cleanup)
    signal.signal(signal.SIGTERM, lambda _sig, _frame: sys.exit(0))
    signal.signal(signal.SIGINT, lambda _sig, _frame: sys.exit(0))

    # Signal readiness by writing "ready\n" to stdout — the CLI start
    # command watches for this before returning.
    sys.stdout.write("ready\n")
    sys.stdout.flush()

    # Detach stdio after signaling readiness. Without this, the daemon
    # keeps the caller's stdout/stderr pipes open indefinitely, causing
    # captured-subprocess launches (e.g. test harnesses) to hang waiting
    # for EOF on the daemon's inherited file descriptors.
    _detach_stdio()

    _run_loop(session, server_sock, socket_path, pid_path)


def _run_loop(
    session: Session,
    server_sock: socket.socket,
    socket_path: str,
    pid_path: str,
) -> None:
    """Core event loop — interleave socket I/O with session pumping."""
    SELECT_TIMEOUT = 0.016  # ~60 Hz, keeps the PTY responsive

    while True:
        # Wait for socket activity or timeout so we can pump the session.
        readable, _, _ = select.select([server_sock], [], [], SELECT_TIMEOUT)

        if readable:
            try:
                client_sock, _ = server_sock.accept()
            except OSError:
                # Accept can fail transiently (e.g. client disconnected
                # between select and accept).  Just keep looping.
                pass
            else:
                _handle_client(client_sock, session, server_sock, socket_path, pid_path)

        # Pump PTY I/O with zero timeout so the screen state stays fresh
        # between CLI requests without adding another background thread.
        try:
            session.step(0)
        except BooError:
            # Session step can fail if the process has exited and the
            # native layer is unhappy.  Keep the daemon alive so clients
            # can still query the final screen state.
            pass


def _handle_client(
    client_sock: socket.socket,
    session: Session,
    server_sock: socket.socket,
    socket_path: str,
    pid_path: str,
) -> None:
    """Read one JSON command from the client, dispatch it, send a response."""
    try:
        # Read until newline or EOF.  Commands are small, so a single
        # recv is almost always sufficient.
        chunks: list[bytes] = []
        while True:
            chunk = client_sock.recv(4096)
            if not chunk:
                break
            chunks.append(chunk)
            if b"\n" in chunk:
                break

        raw = b"".join(chunks).strip()
        if not raw:
            _send_response(client_sock, {"ok": False, "error": "Empty command"})
            return

        try:
            cmd = json.loads(raw)
        except json.JSONDecodeError as exc:
            _send_response(client_sock, {"ok": False, "error": f"Bad JSON: {exc}"})
            return

        response = _dispatch(cmd, session)
        _send_response(client_sock, response)

        # The "stop" command triggers a clean shutdown after the response
        # has been sent, so the client sees the acknowledgement.
        if cmd.get("cmd") == "stop":
            _cleanup(session, server_sock, socket_path, pid_path)
            sys.exit(0)

    except Exception:
        # Last-resort error handling — don't let a bad client crash the
        # daemon.  Log to stderr (captured by the parent if desired).
        traceback.print_exc()
    finally:
        client_sock.close()


def _dispatch(cmd: dict, session: Session) -> dict:
    """Route a command dict to the appropriate Session method."""
    action = cmd.get("cmd")

    if action == "screen":
        trim = cmd.get("trim", True)
        unwrap = cmd.get("unwrap", False)
        text = session.screen_text(trim=trim, unwrap=unwrap)
        return {"ok": True, "text": text}

    if action == "type":
        text = cmd.get("text", "")
        if not text:
            return {"ok": False, "error": "Missing 'text' field"}
        session.send_text(text)
        return {"ok": True}

    if action == "press":
        key = cmd.get("key", "")
        if not key:
            return {"ok": False, "error": "Missing 'key' field"}
        session.send_key(
            key,
            ctrl=cmd.get("ctrl", False),
            alt=cmd.get("alt", False),
            shift=cmd.get("shift", False),
        )
        return {"ok": True}

    if action == "stop":
        # Terminate the child process gracefully, then the caller
        # (_handle_client) will do the actual shutdown.
        try:
            session.terminate()
        except BooError:
            pass  # Already dead — that's fine.
        return {"ok": True}

    if action == "alive":
        return {"ok": True, "alive": session.is_alive()}

    return {"ok": False, "error": f"Unknown command: {action!r}"}


def _send_response(client_sock: socket.socket, response: dict) -> None:
    """Write a newline-terminated JSON response to the client."""
    payload = json.dumps(response) + "\n"
    try:
        client_sock.sendall(payload.encode("utf-8"))
    except BrokenPipeError:
        pass  # Client hung up before reading — nothing we can do.


def _remove_stale_socket(socket_path: str) -> None:
    """Remove a leftover socket file if no process owns it."""
    if not os.path.exists(socket_path):
        return

    pid_path = _pid_path_for(socket_path)
    if os.path.exists(pid_path):
        try:
            old_pid = int(open(pid_path).read().strip())
            # Check whether that PID is still alive.
            os.kill(old_pid, 0)
            # If we get here, a daemon is already running.
            raise BooError(
                f"A daemon is already running (PID {old_pid}).  "
                f"Stop it with 'boo stop' or remove {socket_path} manually."
            )
        except ProcessLookupError:
            pass  # Old process is gone — safe to clean up.
        except PermissionError:
            # Process exists but we can't signal it — assume it's alive.
            raise BooError(
                f"A daemon may already be running (PID file {pid_path} exists "
                f"and the process is not ours).  Remove it manually if stale."
            )
        os.unlink(pid_path)

    os.unlink(socket_path)


def _pid_path_for(socket_path: str) -> str:
    """Derive the PID file path from the socket path."""
    return socket_path.rsplit(".", 1)[0] + ".pid"


def _write_pid_file(pid_path: str) -> None:
    with open(pid_path, "w") as f:
        f.write(str(os.getpid()))


def _cleanup(
    session: Session,
    server_sock: socket.socket,
    socket_path: str,
    pid_path: str,
) -> None:
    """Tear down session, socket, and pid file."""
    try:
        session.close()
    except Exception:
        pass
    try:
        server_sock.close()
    except Exception:
        pass
    try:
        os.unlink(socket_path)
    except FileNotFoundError:
        pass
    try:
        os.unlink(pid_path)
    except FileNotFoundError:
        pass


def _detach_stdio() -> None:
    """Redirect stdin/stdout/stderr to /dev/null after startup.

    The daemon inherits the caller's file descriptors. If the caller captured
    stdout/stderr (e.g. subprocess.PIPE in a test), keeping them open prevents
    the caller from seeing EOF — the test hangs forever waiting for the pipe
    to close. Redirecting to /dev/null after the readiness signal is sent
    breaks that dependency while still allowing the daemon to run normally.
    """
    devnull = os.open(os.devnull, os.O_RDWR)
    os.dup2(devnull, 0)  # stdin
    os.dup2(devnull, 1)  # stdout
    os.dup2(devnull, 2)  # stderr
    os.close(devnull)

    # Replace Python's sys.std* objects so any Python-level writes also go
    # to /dev/null instead of raising errors on the closed fds.
    sys.stdin = open(os.devnull, "r")
    sys.stdout = open(os.devnull, "w")
    sys.stderr = open(os.devnull, "w")


# ---------------------------------------------------------------------------
# Allow the daemon to be launched as ``python -m boo.daemon``.  The CLI
# start command uses this to fork the daemon into a background process.
# ---------------------------------------------------------------------------

def _main() -> None:
    import argparse

    parser = argparse.ArgumentParser(description="Boo session daemon (internal)")
    parser.add_argument("--command", required=True, help="Shell command to run")
    parser.add_argument("--socket", default=DEFAULT_SOCKET_PATH)
    parser.add_argument("--cols", type=int, default=80)
    parser.add_argument("--rows", type=int, default=24)
    args = parser.parse_args()

    run_daemon(
        args.command,
        socket_path=args.socket,
        cols=args.cols,
        rows=args.rows,
    )


if __name__ == "__main__":
    _main()
