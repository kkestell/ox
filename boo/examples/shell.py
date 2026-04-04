from __future__ import annotations

import os

import boo


def run_command(
    session: boo.Session,
    command: str,
    *,
    delay: float = 0.03,
    settle: float = 0.3,
) -> None:
    # `type()` and `press()` are the ergonomic layer: they read like intent,
    # but still route through the same low-level transcript and PTY plumbing.
    session.type(command, delay=delay)
    session.press("enter", wait_for=boo.idle(settle, timeout=3.0))


def main() -> None:
    with boo.launch(
        # Let Boo resolve the user's default shell so the visible demo still
        # feels like a real terminal, not a synthetic fixture.
        None,
        cols=100,
        rows=28,
        visible=True,
        window_title="Boo",
    ) as session:
        session.wait(1.0)

        with session.capture(include_output=False) as launch_capture:
            run_command(
                session,
                "printf 'Boo is driving a real interactive shell.\\n'",
            )
        print("Launch input bytes:", launch_capture.input_hex())

        run_command(session, "printf 'cwd: %s\\n' \"$PWD\"")
        run_command(session, "date '+time: %H:%M:%S'")

        session.press("l", ctrl=True, wait_for=boo.idle(0.3, timeout=2.0))
        run_command(session, "printf 'input bytes so far: %s\\n' \"$(printf %s foo)\"")

        if os.environ.get("SDL_VIDEODRIVER") == "dummy":
            session.terminate()
            session.wait(0.5)
            return

        print("Boo window is live. Close the shell or window when you're done.")
        session.run_until_exit()


if __name__ == "__main__":
    main()
