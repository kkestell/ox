from __future__ import annotations

import os

os.environ.setdefault("SDL_VIDEODRIVER", "dummy")

import boo


def assert_contains(haystack: str, needle: str) -> None:
    if needle not in haystack:
        raise AssertionError(f"Expected {needle!r} in screen:\n{haystack}")


def normalize_whitespace(text: str) -> str:
    return " ".join(text.split())


def assert_screen_matches_bytes(screen: str, data: bytes) -> None:
    assert_contains(normalize_whitespace(screen), boo.hex_bytes(data))


def test_basic_flow() -> None:
    with boo.launch(
        "printf ready; read x; printf seen:$x",
        visible=False,
    ) as session:
        assert_contains(session.wait_for_text("ready", timeout=2), "ready")
        screen = session.type("hello\n", wait_for="seen:hello")
        assert isinstance(screen, str)
        assert_contains(screen, "seen:hello")
        assert session.run_until_exit() == 0


def test_layout_snapshot() -> None:
    with boo.launch(
        "printf 'abcde\\r\\n12345'; printf '\\033[1;3H'; printf 'Z'",
        cols=5,
        rows=2,
        visible=False,
    ) as session:
        session.step(200)
        screen = session.screen_text(trim=True, unwrap=False)
        assert screen == "abZde\n12345", screen


def test_resize() -> None:
    with boo.launch(
        "printf before:; stty size; read x; printf after:; stty size",
        visible=False,
    ) as session:
        session.wait_for_text("before:", timeout=2)
        session.resize(100, 30)
        session.send_text("\n")
        screen = session.wait_for_text("after:", timeout=2)
        assert_contains(screen, "before:24 80")
        assert_contains(screen, "after:30 100")


def test_input_transcript_for_keys() -> None:
    with boo.launch(
        "stty -echo -icanon -isig min 1 time 0; printf ready; "
        "dd bs=1 count=3 2>/dev/null | od -An -tx1",
        visible=False,
    ) as session:
        session.wait_for_text("ready", timeout=2)
        with session.capture(include_output=False) as capture:
            session.send_key("up")
        screen = session.wait_for_text("41", timeout=2)
        assert_contains(normalize_whitespace(screen), "1b 5b 41")
        assert capture.input == b"\x1b[A"
        assert capture.output == b""
        assert session.input_bytes().endswith(b"\x1b[A")
        assert session.output_bytes() == b""


def test_send_bytes_and_output_capture() -> None:
    with boo.launch(
        "stty -echo -icanon -isig min 1 time 0; "
        "printf ready; "
        "dd bs=1 count=4 2>/dev/null | od -An -tx1; "
        "dd bs=1 count=1 2>/dev/null | od -An -tx1",
        visible=False,
    ) as session:
        session.wait_for_text("ready", timeout=2)
        assert session.output_bytes() == b""
        with session.capture() as capture:
            session.send_bytes(b"\x00A\nB")
            screen = session.wait_for_text("42", timeout=2)
        assert_contains(normalize_whitespace(screen), "00 41 0a 42")
        assert capture.input == b"\x00A\nB"
        assert b"00" in capture.output
        assert b"42" in capture.output
        assert b"ready" not in capture.output
        assert session.input_bytes().endswith(b"\x00A\nB")
        assert b"ready" not in session.output_bytes()
        session.send_bytes(b"Z")
        marker_screen = session.wait_for_text("5a", timeout=2)
        assert_contains(normalize_whitespace(marker_screen), "5a")
        assert b"5a" not in session.output_bytes()


def test_full_session_output_capture_is_opt_in() -> None:
    with boo.launch("printf ready", visible=False, capture_output=True) as session:
        session.wait_for_text("ready", timeout=2)
        assert b"ready" in session.output_bytes()


def test_send_bytes_fails_when_the_child_is_not_reading() -> None:
    with boo.launch("sleep 1", visible=False) as session:
        previous_limit = os.environ.get("BOO_TEST_WRITE_LIMIT")
        os.environ["BOO_TEST_WRITE_LIMIT"] = "1"
        try:
            session.send_bytes(b"xyz")
        except boo.BooError as exc:
            assert_contains(str(exc), "send_bytes wrote")
        else:
            raise AssertionError("Expected send_bytes to fail on a short write")
        finally:
            if previous_limit is None:
                os.environ.pop("BOO_TEST_WRITE_LIMIT", None)
            else:
                os.environ["BOO_TEST_WRITE_LIMIT"] = previous_limit


def test_wait_for_idle() -> None:
    with boo.launch(
        "printf ready; read x; printf first; sleep 0.1; printf second; read y",
        visible=False,
    ) as session:
        session.wait_for_text("ready", timeout=2)
        activity = session.press(
            "enter",
            wait_for=boo.idle(0.2, timeout=1.0, poll_interval=0.02),
        )
        assert isinstance(activity, boo.Activity)
        assert activity.output_bytes >= len(b"readyfirstsecond")
        assert activity.output_quiet_ms >= 200
        assert_contains(session.screen_text(trim=True, unwrap=False), "second")


def test_wait_for_text_timeout_includes_context() -> None:
    with boo.launch("printf ready; sleep 0.2", visible=False) as session:
        session.wait_for_text("ready", timeout=2)
        try:
            session.wait_for_text("never", timeout=0.05, poll_interval=0.01)
        except boo.WaitTimeoutError as exc:
            message = str(exc)
            assert_contains(message, "Timed out waiting for 'never'")
            assert_contains(message, "Activity:")
            assert_contains(message, "Last screen:")
        else:
            raise AssertionError("Expected wait_for_text to time out")


def test_wait_for_idle_process_exit_includes_context() -> None:
    with boo.launch("printf done", visible=False, capture_output=True) as session:
        session.wait_for_text("done", timeout=2)
        try:
            session.wait_for_idle(idle_for=0.5, timeout=1.0, poll_interval=0.01)
        except boo.ProcessExitedError as exc:
            message = str(exc)
            assert_contains(message, "Process exited before terminal output went idle.")
            assert_contains(message, "Activity:")
            assert_contains(message, "Last screen:")
        else:
            raise AssertionError("Expected wait_for_idle to fail when the child exits")


def test_scripted_mouse_click() -> None:
    expected = b"\x1b[<0;2;2M\x1b[<0;2;2m"
    with boo.launch(
        f"stty -echo -icanon -isig min 1 time 0; "
        "printf '\\033[?1000h\\033[?1006h'; "
        "printf ready; "
        f"dd bs=1 count={len(expected)} 2>/dev/null | od -An -tx1",
        visible=False,
    ) as session:
        session.wait_for_text("ready", timeout=2)
        with session.capture(include_output=False) as capture:
            session.click(1, 1)
        screen = session.wait_for_text("1b", timeout=2)
        assert capture.input == expected
        assert_screen_matches_bytes(screen, expected)


def test_scripted_mouse_drag_motion() -> None:
    expected = b"\x1b[<0;1;1M\x1b[<32;3;2M\x1b[<0;3;2m"
    with boo.launch(
        f"stty -echo -icanon -isig min 1 time 0; "
        "printf '\\033[?1003h\\033[?1006h'; "
        "printf ready; "
        f"dd bs=1 count={len(expected)} 2>/dev/null | od -An -tx1",
        visible=False,
    ) as session:
        session.wait_for_text("ready", timeout=2)
        with session.capture(include_output=False) as capture:
            session.mouse_down(0, 0)
            session.mouse_move(2, 1)
            session.mouse_up(2, 1)
        screen = session.wait_for_text("1b", timeout=2)
        assert capture.input == expected
        assert_screen_matches_bytes(screen, expected)


def test_scripted_mouse_scroll() -> None:
    expected = b"\x1b[<64;2;2M\x1b[<64;2;2m\x1b[<67;2;2M\x1b[<67;2;2m"
    with boo.launch(
        f"stty -echo -icanon -isig min 1 time 0; "
        "printf '\\033[?1000h\\033[?1006h'; "
        "printf ready; "
        f"dd bs=1 count={len(expected)} 2>/dev/null | od -An -tx1",
        visible=False,
    ) as session:
        session.wait_for_text("ready", timeout=2)
        with session.capture(include_output=False) as capture:
            session.scroll(1, 1, delta_y=1, delta_x=-1)
        screen = session.wait_for_text("1b", timeout=2)
        assert capture.input == expected
        assert_screen_matches_bytes(screen, expected)


def main() -> None:
    test_basic_flow()
    test_layout_snapshot()
    test_resize()
    test_input_transcript_for_keys()
    test_send_bytes_and_output_capture()
    test_full_session_output_capture_is_opt_in()
    test_send_bytes_fails_when_the_child_is_not_reading()
    test_wait_for_idle()
    test_wait_for_text_timeout_includes_context()
    test_wait_for_idle_process_exit_includes_context()
    test_scripted_mouse_click()
    test_scripted_mouse_drag_motion()
    test_scripted_mouse_scroll()


if __name__ == "__main__":
    main()
