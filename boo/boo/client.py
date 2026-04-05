"""Thin client for communicating with the boo session daemon.

Connects to the daemon's Unix domain socket, sends a single JSON command,
reads back a JSON response, and disconnects.  Each CLI invocation opens a
fresh connection — there is no persistent client state.
"""

from __future__ import annotations

import json
import os
import socket

from boo import BooError

# Default socket path — uses $USER so multiple users on the same machine
# don't collide.
DEFAULT_SOCKET_PATH = f"/tmp/boo-{os.environ.get('USER', 'unknown')}.sock"


class DaemonNotRunningError(BooError):
    """Raised when the daemon socket doesn't exist or refuses connection."""
    pass


def send_command(
    cmd: dict,
    *,
    socket_path: str = DEFAULT_SOCKET_PATH,
) -> dict:
    """Send a JSON command to the daemon and return the JSON response.

    Opens a Unix domain socket connection, writes a newline-delimited JSON
    object, reads the response (also newline-delimited JSON), and closes the
    connection.  The daemon handles one command per connection.

    Raises DaemonNotRunningError if the socket doesn't exist or connection
    is refused.  Raises BooError if the response has ``"ok": false``.
    """
    if not os.path.exists(socket_path):
        raise DaemonNotRunningError(
            f"Daemon socket not found at {socket_path} — is the daemon running?"
        )

    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        try:
            sock.connect(socket_path)
        except ConnectionRefusedError:
            raise DaemonNotRunningError(
                f"Connection refused at {socket_path} — is the daemon running?"
            )

        # Send command as newline-terminated JSON.
        payload = json.dumps(cmd) + "\n"
        sock.sendall(payload.encode("utf-8"))

        # Read response — accumulate until we see a newline or EOF.
        chunks: list[bytes] = []
        while True:
            chunk = sock.recv(4096)
            if not chunk:
                break
            chunks.append(chunk)
            # Stop as soon as we have a complete line.
            if b"\n" in chunk:
                break

        raw = b"".join(chunks).strip()
        if not raw:
            raise BooError("Empty response from daemon")

        response = json.loads(raw)
    finally:
        sock.close()

    # Surface daemon-side errors as exceptions so callers don't need to
    # check the response dict themselves.
    if not response.get("ok", False):
        error_msg = response.get("error", "Unknown daemon error")
        raise BooError(error_msg)

    return response
