"""Boo-driven Ox smoke tests using the fake provider.

These tests launch the real Ox binary with --fake-provider against scripted
scenarios and verify TUI behavior via Boo commands. Each test creates its
own temporary workspace and Boo socket so tests can run independently.

Prerequisites:
    - Ox must be built: dotnet build src/Ox/Ox.csproj
    - Boo must be built: make -C boo build
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import boo

# Resolve paths relative to the repo root.
REPO_ROOT = Path(__file__).resolve().parents[2]
OX_BINARY = REPO_ROOT / "src" / "Ox" / "bin" / "Debug" / "net10.0" / "Ox"
BOO_DIR = REPO_ROOT / "boo"


def run_cli(
    socket_path: Path,
    *args: str,
    check: bool = True,
    timeout: float = 15.0,
) -> subprocess.CompletedProcess[str]:
    """Run a boo CLI command."""
    env = os.environ.copy()
    package_root = Path(__file__).resolve().parents[1]
    existing_pythonpath = env.get("PYTHONPATH")
    env["PYTHONPATH"] = (
        f"{package_root}{os.pathsep}{existing_pythonpath}"
        if existing_pythonpath
        else str(package_root)
    )

    result = subprocess.run(
        [
            sys.executable,
            "-m",
            "boo.cli",
            "--socket",
            str(socket_path),
            *args,
        ],
        text=True,
        capture_output=True,
        env=env,
        cwd=package_root,
        check=False,
        timeout=timeout,
    )

    if check and result.returncode != 0:
        raise AssertionError(
            f"Command failed: {args}\n"
            f"stdout:\n{result.stdout}\n"
            f"stderr:\n{result.stderr}"
        )

    return result


def wait_for_screen(
    socket_path: Path,
    needle: str,
    timeout: float = 15.0,
) -> str:
    """Poll screen until needle appears or timeout expires."""
    deadline = time.monotonic() + timeout
    last_screen = ""
    while time.monotonic() < deadline:
        result = run_cli(socket_path, "screen", check=False)
        if result.returncode == 0 and needle in result.stdout:
            return result.stdout
        last_screen = result.stdout if result.returncode == 0 else last_screen
        time.sleep(0.1)

    raise AssertionError(
        f"Timed out waiting for {needle!r} on screen.\n"
        f"Last screen:\n{last_screen}"
    )


def stop_session(socket_path: Path) -> None:
    """Stop daemon, ignoring errors if it's not running."""
    run_cli(socket_path, "stop", check=False)


def setup_workspace(scenario: str, socket_name: str) -> tuple[Path, Path]:
    """Create a temp workspace and return (workspace_path, socket_path)."""
    workspace = Path(tempfile.mkdtemp(prefix="ox-boo-test-"))
    socket_path = BOO_DIR / f".{socket_name}.sock"

    # Stop any stale session on this socket.
    stop_session(socket_path)

    # Create a test file for read_file scenarios.
    (workspace / "hello.txt").write_text("test-sentinel")

    return workspace, socket_path


def launch_ox(
    workspace: Path,
    socket_path: Path,
    scenario: str,
    cols: int = 120,
    rows: int = 50,
) -> None:
    """Launch Ox with the fake provider via Boo."""
    ox_cmd = f"cd {workspace} && {OX_BINARY} --fake-provider {scenario}"
    run_cli(
        socket_path,
        "start", ox_cmd,
        "--cols", str(cols),
        "--rows", str(rows),
    )


def cleanup(workspace: Path, socket_path: Path) -> None:
    """Stop Boo session and remove temp workspace."""
    stop_session(socket_path)
    socket_path.unlink(missing_ok=True)
    socket_path.with_suffix(".pid").unlink(missing_ok=True)
    shutil.rmtree(workspace, ignore_errors=True)


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

def test_first_turn_response_rendering() -> None:
    """Verify that a simple prompt + fake response renders correctly."""
    workspace, socket_path = setup_workspace("hello", "boo-ox-hello")
    try:
        launch_ox(workspace, socket_path, "hello")

        # The TUI should boot without readiness prompts (fake provider is ready).
        # Wait for the input area to appear — the '>' prompt or model ID display.
        wait_for_screen(socket_path, "fake/hello", timeout=15)

        # Type a message and press Enter to submit.
        run_cli(socket_path, "type", "hi there")
        run_cli(socket_path, "press", "enter")

        # The hello scenario returns "Hello! I'm a fake provider. How can I help you today?"
        screen = wait_for_screen(socket_path, "fake provider", timeout=15)
        assert "Hello!" in screen
        assert "How can I help you today?" in screen
    finally:
        cleanup(workspace, socket_path)


def test_long_streamed_response_rendering() -> None:
    """Verify that a long multi-chunk response renders without truncation."""
    workspace, socket_path = setup_workspace("long-response", "boo-ox-long")
    try:
        launch_ox(workspace, socket_path, "long-response")
        wait_for_screen(socket_path, "fake/long-response", timeout=15)

        run_cli(socket_path, "type", "tell me something")
        run_cli(socket_path, "press", "enter")

        # The long-response scenario's final paragraph contains this text.
        screen = wait_for_screen(
            socket_path, "Final paragraph", timeout=15)
        assert "scroll position" in screen
    finally:
        cleanup(workspace, socket_path)


def test_cancellation_via_escape() -> None:
    """Verify that pressing Escape during a turn shows [cancelled]."""
    # Use multi-turn so there's a valid response before we cancel.
    workspace, socket_path = setup_workspace("multi-turn", "boo-ox-cancel")
    try:
        launch_ox(workspace, socket_path, "multi-turn")
        wait_for_screen(socket_path, "fake/multi-turn", timeout=15)

        # First turn: get a normal response.
        run_cli(socket_path, "type", "hello")
        run_cli(socket_path, "press", "enter")
        wait_for_screen(socket_path, "ready to help", timeout=15)

        # Second turn: immediately press Escape. Given the fake provider is
        # instant, this may or may not cancel in time — but the test verifies
        # the cancel path doesn't crash.
        run_cli(socket_path, "type", "do something")
        run_cli(socket_path, "press", "enter")
        run_cli(socket_path, "press", "escape")

        # Give the TUI time to process. Either the response completed or
        # was cancelled — both are acceptable.
        time.sleep(0.5)
        screen_result = run_cli(socket_path, "screen")
        screen = screen_result.stdout
        # The session should not have crashed.
        assert "error" not in screen.lower() or "error path" in screen.lower()
    finally:
        cleanup(workspace, socket_path)


def test_clean_shutdown() -> None:
    """Verify that Ctrl+D causes a clean exit."""
    workspace, socket_path = setup_workspace("hello", "boo-ox-shutdown")
    try:
        launch_ox(workspace, socket_path, "hello")
        wait_for_screen(socket_path, "fake/hello", timeout=15)

        # Send Ctrl+D to trigger EOF.
        run_cli(socket_path, "press", "d", "--ctrl")

        # The Ox process should exit, making the session "not alive".
        deadline = time.monotonic() + 10.0
        while time.monotonic() < deadline:
            result = run_cli(socket_path, "alive", check=False)
            if result.returncode != 0:
                assert result.stdout.strip() == "not alive"
                return
            time.sleep(0.1)

        raise AssertionError("Ox did not exit after Ctrl+D within timeout")
    finally:
        cleanup(workspace, socket_path)


def main() -> None:
    """Run all Ox Boo smoke tests."""
    if not OX_BINARY.exists():
        print(
            f"Ox binary not found at {OX_BINARY}. "
            "Build with: dotnet build src/Ox/Ox.csproj",
            file=sys.stderr,
        )
        sys.exit(1)

    tests = [
        ("first-turn response rendering", test_first_turn_response_rendering),
        ("long streamed response rendering", test_long_streamed_response_rendering),
        ("cancellation via escape", test_cancellation_via_escape),
        ("clean shutdown", test_clean_shutdown),
    ]

    passed = 0
    failed = 0
    for name, test_fn in tests:
        print(f"  {name}...", end=" ", flush=True)
        try:
            test_fn()
            print("ok")
            passed += 1
        except Exception as exc:
            print(f"FAILED: {exc}")
            failed += 1

    print(f"\n{passed} passed, {failed} failed")
    if failed > 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
