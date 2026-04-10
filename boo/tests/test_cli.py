from __future__ import annotations

import os
import subprocess
import sys
import time
from pathlib import Path

import boo


TEST_SOCKET_DIR = Path(__file__).resolve().parents[1]


def run_cli(
    socket_path: Path,
    *args: str,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
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
    )

    if check and result.returncode != 0:
        raise AssertionError(
            f"Command failed: {args}\n"
            f"stdout:\n{result.stdout}\n"
            f"stderr:\n{result.stderr}"
        )

    return result


def wait_for_screen(socket_path: Path, needle: str, timeout: float = 3.0) -> str:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = run_cli(socket_path, "screen")
        if needle in result.stdout:
            return result.stdout
        time.sleep(0.05)

    raise AssertionError(f"Timed out waiting for {needle!r} on the screen")


def stop_session(socket_path: Path) -> None:
    run_cli(socket_path, "stop", check=False)


def test_package_exports_cli_only() -> None:
    assert not hasattr(boo, "launch")
    assert not hasattr(boo, "Session")


def test_cli_basic_flow() -> None:
    socket_path = TEST_SOCKET_DIR / ".boo-test-basic.sock"
    stop_session(socket_path)

    try:
        run_cli(
            socket_path,
            "start",
            "printf ready; read x; printf seen:$x",
        )

        wait_for_screen(socket_path, "ready")
        run_cli(socket_path, "type", "hello\n")
        screen = wait_for_screen(socket_path, "seen:hello")
        assert "seen:hello" in screen

        alive = run_cli(socket_path, "alive")
        assert alive.stdout.strip() == "alive"
    finally:
        stop_session(socket_path)
        socket_path.unlink(missing_ok=True)
        socket_path.with_suffix(".pid").unlink(missing_ok=True)


def test_cli_press_ctrl_d_reports_not_alive() -> None:
    socket_path = TEST_SOCKET_DIR / ".boo-test-exit.sock"
    stop_session(socket_path)

    try:
        run_cli(socket_path, "start", "cat")
        run_cli(socket_path, "press", "d", "--ctrl")

        deadline = time.monotonic() + 3.0
        while time.monotonic() < deadline:
            alive = run_cli(socket_path, "alive", check=False)
            if alive.returncode != 0:
                assert alive.stdout.strip() == "not alive"
                return
            time.sleep(0.05)

        raise AssertionError("Timed out waiting for the session to exit")
    finally:
        stop_session(socket_path)
        socket_path.unlink(missing_ok=True)
        socket_path.with_suffix(".pid").unlink(missing_ok=True)


def main() -> None:
    test_package_exports_cli_only()
    test_cli_basic_flow()
    test_cli_press_ctrl_d_reports_not_alive()


if __name__ == "__main__":
    main()
