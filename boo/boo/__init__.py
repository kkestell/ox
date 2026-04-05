from __future__ import annotations

import ctypes
import os
import time
from ctypes.util import find_library
from dataclasses import dataclass
from pathlib import Path
from typing import Mapping, Sequence


class BooError(RuntimeError):
    pass


class ProcessExitedError(BooError):
    pass


class WaitTimeoutError(BooError):
    pass


@dataclass(frozen=True)
class Activity:
    input_bytes: int
    output_bytes: int
    input_quiet_ms: int
    output_quiet_ms: int


@dataclass(frozen=True)
class Idle:
    idle_for: float = 0.25
    timeout: float = 5.0
    poll_interval: float = 0.05


@dataclass(frozen=True)
class CaptureResult:
    input: bytes
    output: bytes

    def input_hex(self, *, separator: str = " ") -> str:
        return hex_bytes(self.input, separator=separator)

    def output_hex(self, *, separator: str = " ") -> str:
        return hex_bytes(self.output, separator=separator)


class Capture:
    """Offset-based transcript capture layered on top of full session buffers.

    The native layer owns the append-only byte transcripts. This object records
    offsets at the start so the Python API can expose "capture this interaction"
    without teaching the C layer about arbitrary capture windows.
    """

    def __init__(
        self,
        session: Session,
        *,
        include_input: bool = True,
        include_output: bool = True,
    ) -> None:
        self._session = session
        self._include_input = include_input
        self._include_output = include_output
        self._restore_output_capture = False
        if include_output and not session._output_capture_enabled:
            session._set_output_capture(True)
            self._restore_output_capture = True
        self._input_start = len(session.input_bytes()) if include_input else 0
        self._output_start = len(session.output_bytes()) if include_output else 0
        self._result: CaptureResult | None = None

    def stop(self) -> CaptureResult:
        if self._result is None:
            input_bytes = b""
            output_bytes = b""
            if self._include_input:
                input_bytes = self._session.input_bytes()[self._input_start :]
            if self._include_output:
                output_bytes = self._session.output_bytes()[self._output_start :]
            self._result = CaptureResult(input=input_bytes, output=output_bytes)
            if self._restore_output_capture:
                self._session._set_output_capture(False)
        return self._result

    @property
    def input(self) -> bytes:
        return self.stop().input

    @property
    def output(self) -> bytes:
        return self.stop().output

    def input_hex(self, *, separator: str = " ") -> str:
        return self.stop().input_hex(separator=separator)

    def output_hex(self, *, separator: str = " ") -> str:
        return self.stop().output_hex(separator=separator)

    def __enter__(self) -> Capture:
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.stop()


def idle(
    idle_for: float = 0.25,
    *,
    timeout: float = 5.0,
    poll_interval: float = 0.05,
) -> Idle:
    return Idle(idle_for=idle_for, timeout=timeout, poll_interval=poll_interval)


def hex_bytes(data: bytes, *, separator: str = " ") -> str:
    return separator.join(f"{byte:02x}" for byte in data)


class _LaunchOptions(ctypes.Structure):
    _fields_ = [
        ("size", ctypes.c_size_t),
        ("cols", ctypes.c_uint16),
        ("rows", ctypes.c_uint16),
        ("font_size", ctypes.c_int),
        ("padding", ctypes.c_int),
        ("cwd", ctypes.c_char_p),
        ("env", ctypes.POINTER(ctypes.c_char_p)),
        ("visible", ctypes.c_bool),
        ("window_title", ctypes.c_char_p),
    ]


class _ActivitySnapshot(ctypes.Structure):
    _fields_ = [
        ("size", ctypes.c_size_t),
        ("input_bytes", ctypes.c_uint64),
        ("output_bytes", ctypes.c_uint64),
        ("input_quiet_ms", ctypes.c_uint64),
        ("output_quiet_ms", ctypes.c_uint64),
    ]


class _Native:
    def __init__(self) -> None:
        self.lib = ctypes.CDLL(str(self._resolve_library()))
        self._configure()

    def _resolve_library(self) -> Path:
        override = os.environ.get("BOO_NATIVE_LIB")
        if override:
            return Path(override).expanduser().resolve()

        module_dir = Path(__file__).resolve().parent
        candidates = [
            module_dir / "libboo_tester.so",
            module_dir / "libboo_tester.dylib",
            module_dir / "boo_tester.dll",
        ]
        for candidate in candidates:
            if candidate.exists():
                return candidate

        system_library = find_library("boo_tester")
        if system_library:
            return Path(system_library)

        raise BooError(
            "Could not find libboo_tester; build the project to place it next to boo/__init__.py, "
            "install it somewhere the dynamic loader can find it, or set BOO_NATIVE_LIB."
        )

    def _configure(self) -> None:
        self.lib.boo_session_new.restype = ctypes.c_void_p
        self.lib.boo_session_free.argtypes = [ctypes.c_void_p]
        self.lib.boo_session_last_error.argtypes = [ctypes.c_void_p]
        self.lib.boo_session_last_error.restype = ctypes.c_char_p

        self.lib.boo_session_launch.argtypes = [
            ctypes.c_void_p,
            ctypes.POINTER(ctypes.c_char_p),
            ctypes.POINTER(_LaunchOptions),
        ]
        self.lib.boo_session_launch.restype = ctypes.c_int

        self.lib.boo_session_step.argtypes = [ctypes.c_void_p, ctypes.c_int]
        self.lib.boo_session_step.restype = ctypes.c_int

        self.lib.boo_session_send_bytes.argtypes = [
            ctypes.c_void_p,
            ctypes.c_void_p,
            ctypes.c_size_t,
        ]
        self.lib.boo_session_send_bytes.restype = ctypes.c_int

        self.lib.boo_session_send_text.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
        self.lib.boo_session_send_text.restype = ctypes.c_int

        self.lib.boo_session_send_key.argtypes = [
            ctypes.c_void_p,
            ctypes.c_int,
            ctypes.c_uint16,
        ]
        self.lib.boo_session_send_key.restype = ctypes.c_int

        self.lib.boo_session_send_key_action.argtypes = [
            ctypes.c_void_p,
            ctypes.c_int,
            ctypes.c_uint16,
            ctypes.c_int,
        ]
        self.lib.boo_session_send_key_action.restype = ctypes.c_int

        self.lib.boo_session_send_mouse_button.argtypes = [
            ctypes.c_void_p,
            ctypes.c_int,
            ctypes.c_int,
            ctypes.c_int,
            ctypes.c_uint16,
            ctypes.c_bool,
        ]
        self.lib.boo_session_send_mouse_button.restype = ctypes.c_int

        self.lib.boo_session_send_mouse_move.argtypes = [
            ctypes.c_void_p,
            ctypes.c_int,
            ctypes.c_int,
            ctypes.c_uint16,
        ]
        self.lib.boo_session_send_mouse_move.restype = ctypes.c_int

        self.lib.boo_session_send_mouse_wheel.argtypes = [
            ctypes.c_void_p,
            ctypes.c_int,
            ctypes.c_int,
            ctypes.c_int,
            ctypes.c_int,
            ctypes.c_uint16,
        ]
        self.lib.boo_session_send_mouse_wheel.restype = ctypes.c_int

        self.lib.boo_session_snapshot_text.argtypes = [
            ctypes.c_void_p,
            ctypes.c_bool,
            ctypes.c_bool,
        ]
        self.lib.boo_session_snapshot_text.restype = ctypes.c_void_p

        self.lib.boo_session_snapshot_input.argtypes = [
            ctypes.c_void_p,
            ctypes.POINTER(ctypes.c_size_t),
        ]
        self.lib.boo_session_snapshot_input.restype = ctypes.c_void_p

        self.lib.boo_session_snapshot_output.argtypes = [
            ctypes.c_void_p,
            ctypes.POINTER(ctypes.c_size_t),
        ]
        self.lib.boo_session_snapshot_output.restype = ctypes.c_void_p

        self.lib.boo_session_snapshot_activity.argtypes = [
            ctypes.c_void_p,
            ctypes.POINTER(_ActivitySnapshot),
        ]
        self.lib.boo_session_snapshot_activity.restype = ctypes.c_int

        self.lib.boo_session_set_output_capture.argtypes = [
            ctypes.c_void_p,
            ctypes.c_bool,
        ]
        self.lib.boo_session_set_output_capture.restype = ctypes.c_int

        self.lib.boo_string_free.argtypes = [ctypes.c_void_p]
        self.lib.boo_buffer_free.argtypes = [ctypes.c_void_p]

        self.lib.boo_session_resize.argtypes = [
            ctypes.c_void_p,
            ctypes.c_uint16,
            ctypes.c_uint16,
        ]
        self.lib.boo_session_resize.restype = ctypes.c_int

        self.lib.boo_session_is_alive.argtypes = [ctypes.c_void_p]
        self.lib.boo_session_is_alive.restype = ctypes.c_bool

        self.lib.boo_session_exit_status.argtypes = [ctypes.c_void_p]
        self.lib.boo_session_exit_status.restype = ctypes.c_int

        self.lib.boo_session_terminate.argtypes = [ctypes.c_void_p]
        self.lib.boo_session_terminate.restype = ctypes.c_int


_NATIVE = _Native()


class Session:
    _KEYS = {
        "`": 1,
        "\\": 2,
        "[": 3,
        "]": 4,
        ",": 5,
        "0": 6,
        "1": 7,
        "2": 8,
        "3": 9,
        "4": 10,
        "5": 11,
        "6": 12,
        "7": 13,
        "8": 14,
        "9": 15,
        "=": 16,
        "a": 17,
        "b": 18,
        "c": 19,
        "d": 20,
        "e": 21,
        "f": 22,
        "g": 23,
        "h": 24,
        "i": 25,
        "j": 26,
        "k": 27,
        "l": 28,
        "m": 29,
        "n": 30,
        "o": 31,
        "p": 32,
        "q": 33,
        "r": 34,
        "s": 35,
        "t": 36,
        "u": 37,
        "v": 38,
        "w": 39,
        "x": 40,
        "y": 41,
        "z": 42,
        "-": 43,
        ".": 44,
        "'": 45,
        ";": 46,
        "/": 47,
        "backspace": 48,
        "enter": 49,
        "space": 50,
        "tab": 51,
        "delete": 52,
        "end": 53,
        "home": 54,
        "insert": 55,
        "page_down": 56,
        "pagedown": 56,
        "page_up": 57,
        "pageup": 57,
        "down": 58,
        "left": 59,
        "right": 60,
        "up": 61,
        "escape": 62,
        "esc": 62,
        "f1": 63,
        "f2": 64,
        "f3": 65,
        "f4": 66,
        "f5": 67,
        "f6": 68,
        "f7": 69,
        "f8": 70,
        "f9": 71,
        "f10": 72,
        "f11": 73,
        "f12": 74,
    }

    _MOD_SHIFT = 1 << 0
    _MOD_CTRL = 1 << 1
    _MOD_ALT = 1 << 2
    _MOD_SUPER = 1 << 3

    _KEY_ACTIONS = {
        "press": 0,
        "release": 1,
        "repeat": 2,
        "press_and_release": 3,
    }

    _MOUSE_BUTTONS = {
        "left": 1,
        "right": 2,
        "middle": 3,
        "x1": 4,
        "x2": 5,
    }

    def __init__(self, handle: int) -> None:
        self._handle = ctypes.c_void_p(handle)
        self._closed = False
        self._output_capture_enabled = False

    @classmethod
    def launch(
        cls,
        command: str | Sequence[str] | None = None,
        *,
        cols: int = 80,
        rows: int = 24,
        cwd: str | os.PathLike[str] | None = None,
        env: Mapping[str, str] | None = None,
        font_size: int = 16,
        padding: int = 4,
        capture_output: bool = False,
        visible: bool = True,
        window_title: str = "boo",
    ) -> Session:
        handle = _NATIVE.lib.boo_session_new()
        if not handle:
            raise BooError("boo_session_new failed")

        session = cls(handle)
        try:
            argv_array = cls._marshal_command(command)
            env_array = cls._marshal_env(env)
            options = _LaunchOptions(
                size=ctypes.sizeof(_LaunchOptions),
                cols=cols,
                rows=rows,
                font_size=font_size,
                padding=padding,
                cwd=cls._encode_optional_path(cwd),
                env=env_array,
                visible=visible,
                window_title=window_title.encode("utf-8"),
            )
            # ctypes arrays must stay alive for the native launch call.
            session._argv_array = argv_array
            session._env_array = env_array
            session._check(
                _NATIVE.lib.boo_session_launch(session._handle, argv_array, options)
            )
            if capture_output:
                session._set_output_capture(True)
            return session
        except Exception:
            session.close()
            raise

    @staticmethod
    def _marshal_command(
        command: str | Sequence[str] | None,
    ) -> ctypes.Array[ctypes.c_char_p] | None:
        if command is None:
            return None
        if isinstance(command, str):
            items = ["/bin/sh", "-lc", command]
        else:
            items = [os.fspath(item) for item in command]
            if not items:
                raise BooError("command sequence cannot be empty")
        encoded = [item.encode("utf-8") for item in items]
        array = (ctypes.c_char_p * (len(encoded) + 1))()
        for index, value in enumerate(encoded):
            array[index] = value
        array[len(encoded)] = None
        return array

    @staticmethod
    def _marshal_env(
        env: Mapping[str, str] | None,
    ) -> ctypes.Array[ctypes.c_char_p] | None:
        if env is None:
            return None
        entries = [f"{key}={value}".encode("utf-8") for key, value in env.items()]
        array = (ctypes.c_char_p * (len(entries) + 1))()
        for index, value in enumerate(entries):
            array[index] = value
        array[len(entries)] = None
        return array

    @staticmethod
    def _encode_optional_path(path: str | os.PathLike[str] | None) -> bytes | None:
        if path is None:
            return None
        return os.fspath(path).encode("utf-8")

    def _error_message(self) -> str:
        raw = _NATIVE.lib.boo_session_last_error(self._handle)
        if not raw:
            return "boo native call failed"
        text = raw.decode("utf-8", errors="replace")
        return text or "boo native call failed"

    def _check(self, rc: int) -> None:
        if rc != 0:
            raise BooError(self._error_message())

    def _set_output_capture(self, enabled: bool) -> None:
        self._check(_NATIVE.lib.boo_session_set_output_capture(self._handle, enabled))
        self._output_capture_enabled = enabled

    def _modifier_mask(
        self,
        *,
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
        super_: bool = False,
    ) -> int:
        mods = 0
        if shift:
            mods |= self._MOD_SHIFT
        if ctrl:
            mods |= self._MOD_CTRL
        if alt:
            mods |= self._MOD_ALT
        if super_:
            mods |= self._MOD_SUPER
        return mods

    def _bytes_snapshot(self, fn_name: str) -> bytes:
        length = ctypes.c_size_t()
        fn = getattr(_NATIVE.lib, fn_name)
        ptr = fn(self._handle, ctypes.byref(length))
        if not ptr:
            raise BooError(self._error_message())
        try:
            return ctypes.string_at(ptr, length.value)
        finally:
            _NATIVE.lib.boo_buffer_free(ptr)

    def _format_activity(self, activity: Activity | None = None) -> str:
        snapshot = activity or self.activity()
        return (
            f"Activity: input={snapshot.input_bytes} bytes "
            f"(quiet {snapshot.input_quiet_ms} ms), "
            f"output={snapshot.output_bytes} bytes "
            f"(quiet {snapshot.output_quiet_ms} ms)"
        )

    def _failure_context(self, last_screen: str) -> str:
        lines = [self._format_activity(), "Last screen:", last_screen]
        return "\n".join(lines)

    def _wait_for(
        self,
        wait_for: str | Idle | None,
    ) -> str | Activity | None:
        if wait_for is None:
            return None
        if isinstance(wait_for, str):
            return self.wait_for_text(wait_for)
        if isinstance(wait_for, Idle):
            return self.wait_for_idle(
                idle_for=wait_for.idle_for,
                timeout=wait_for.timeout,
                poll_interval=wait_for.poll_interval,
            )
        raise TypeError("wait_for must be text, boo.idle(...), or None")

    def step(self, timeout_ms: int = 0) -> None:
        self._check(_NATIVE.lib.boo_session_step(self._handle, int(timeout_ms)))

    def wait(self, seconds: float, *, poll_interval: float = 0.016) -> None:
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            remaining = deadline - time.monotonic()
            timeout_ms = max(0, min(int(remaining * 1000), int(poll_interval * 1000)))
            self.step(timeout_ms)

    def pump_for(self, seconds: float, *, poll_interval: float = 0.016) -> None:
        self.wait(seconds, poll_interval=poll_interval)

    def run_until_exit(self, *, poll_interval: float = 0.016) -> int | None:
        while self.is_alive():
            self.step(max(1, int(poll_interval * 1000)))
        self.step(0)
        return self.exit_status()

    def send_bytes(self, data: bytes | bytearray | memoryview) -> None:
        payload = bytes(data)
        if not payload:
            return
        buffer = ctypes.create_string_buffer(payload, len(payload))
        self._check(
            _NATIVE.lib.boo_session_send_bytes(
                self._handle, ctypes.cast(buffer, ctypes.c_void_p), len(payload)
            )
        )

    def send_text(self, text: str) -> None:
        if not isinstance(text, str):
            raise TypeError("text must be a str")
        self._check(_NATIVE.lib.boo_session_send_text(self._handle, text.encode("utf-8")))

    def send_key(
        self,
        key: str,
        *,
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
        super_: bool = False,
        action: str | None = None,
    ) -> None:
        lookup = key.lower()
        if lookup not in self._KEYS:
            raise BooError(f"Unsupported key name: {key!r}")
        mods = self._modifier_mask(
            ctrl=ctrl,
            alt=alt,
            shift=shift,
            super_=super_,
        )

        if action is None:
            self._check(
                _NATIVE.lib.boo_session_send_key(self._handle, self._KEYS[lookup], mods)
            )
            return

        if action not in self._KEY_ACTIONS:
            raise ValueError(
                "action must be one of 'press', 'release', 'repeat', or 'press_and_release'"
            )
        self._check(
            _NATIVE.lib.boo_session_send_key_action(
                self._handle, self._KEYS[lookup], mods, self._KEY_ACTIONS[action]
            )
        )

    def mouse_down(
        self,
        x: int,
        y: int,
        *,
        button: str = "left",
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
        super_: bool = False,
    ) -> None:
        lookup = button.lower()
        if lookup not in self._MOUSE_BUTTONS:
            raise BooError(f"Unsupported mouse button: {button!r}")
        mods = self._modifier_mask(
            ctrl=ctrl,
            alt=alt,
            shift=shift,
            super_=super_,
        )
        self._check(
            _NATIVE.lib.boo_session_send_mouse_button(
                self._handle, x, y, self._MOUSE_BUTTONS[lookup], mods, True
            )
        )

    def mouse_up(
        self,
        x: int,
        y: int,
        *,
        button: str = "left",
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
        super_: bool = False,
    ) -> None:
        lookup = button.lower()
        if lookup not in self._MOUSE_BUTTONS:
            raise BooError(f"Unsupported mouse button: {button!r}")
        mods = self._modifier_mask(
            ctrl=ctrl,
            alt=alt,
            shift=shift,
            super_=super_,
        )
        self._check(
            _NATIVE.lib.boo_session_send_mouse_button(
                self._handle, x, y, self._MOUSE_BUTTONS[lookup], mods, False
            )
        )

    def mouse_move(
        self,
        x: int,
        y: int,
        *,
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
        super_: bool = False,
    ) -> None:
        mods = self._modifier_mask(
            ctrl=ctrl,
            alt=alt,
            shift=shift,
            super_=super_,
        )
        self._check(_NATIVE.lib.boo_session_send_mouse_move(self._handle, x, y, mods))

    def scroll(
        self,
        x: int,
        y: int,
        *,
        delta_y: int = 0,
        delta_x: int = 0,
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
        super_: bool = False,
    ) -> None:
        mods = self._modifier_mask(
            ctrl=ctrl,
            alt=alt,
            shift=shift,
            super_=super_,
        )
        self._check(
            _NATIVE.lib.boo_session_send_mouse_wheel(
                self._handle, x, y, delta_x, delta_y, mods
            )
        )

    def click(
        self,
        x: int,
        y: int,
        *,
        button: str = "left",
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
        super_: bool = False,
        wait_for: str | Idle | None = None,
    ) -> str | Activity | None:
        self.mouse_down(
            x,
            y,
            button=button,
            ctrl=ctrl,
            alt=alt,
            shift=shift,
            super_=super_,
        )
        self.mouse_up(
            x,
            y,
            button=button,
            ctrl=ctrl,
            alt=alt,
            shift=shift,
            super_=super_,
        )
        return self._wait_for(wait_for)

    def press(
        self,
        key: str,
        *,
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
        super_: bool = False,
        wait_for: str | Idle | None = None,
    ) -> str | Activity | None:
        self.send_key(
            key,
            ctrl=ctrl,
            alt=alt,
            shift=shift,
            super_=super_,
        )
        return self._wait_for(wait_for)

    def type(
        self,
        text: str,
        *,
        delay: float | None = None,
        wait_for: str | Idle | None = None,
    ) -> str | Activity | None:
        if delay is None or delay <= 0:
            self.send_text(text)
        else:
            for char in text:
                self.send_text(char)
                self.wait(delay)
        return self._wait_for(wait_for)

    def screen_text(self, *, trim: bool = True, unwrap: bool = False) -> str:
        ptr = _NATIVE.lib.boo_session_snapshot_text(self._handle, trim, unwrap)
        if not ptr:
            raise BooError(self._error_message())
        try:
            return ctypes.string_at(ptr).decode("utf-8", errors="replace")
        finally:
            _NATIVE.lib.boo_string_free(ptr)

    def input_bytes(self) -> bytes:
        return self._bytes_snapshot("boo_session_snapshot_input")

    def output_bytes(self) -> bytes:
        return self._bytes_snapshot("boo_session_snapshot_output")

    def activity(self) -> Activity:
        snapshot = _ActivitySnapshot(size=ctypes.sizeof(_ActivitySnapshot))
        self._check(
            _NATIVE.lib.boo_session_snapshot_activity(
                self._handle, ctypes.byref(snapshot)
            )
        )
        return Activity(
            input_bytes=int(snapshot.input_bytes),
            output_bytes=int(snapshot.output_bytes),
            input_quiet_ms=int(snapshot.input_quiet_ms),
            output_quiet_ms=int(snapshot.output_quiet_ms),
        )

    def capture(
        self,
        *,
        include_input: bool = True,
        include_output: bool = True,
    ) -> Capture:
        return Capture(
            self,
            include_input=include_input,
            include_output=include_output,
        )

    def wait_for_text(
        self,
        text: str,
        *,
        timeout: float = 5.0,
        poll_interval: float = 0.05,
        trim: bool = True,
        unwrap: bool = False,
    ) -> str:
        deadline = time.monotonic() + timeout
        last_screen = self.screen_text(trim=trim, unwrap=unwrap)
        if text in last_screen:
            return last_screen

        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise WaitTimeoutError(
                    f"Timed out waiting for {text!r}.\n{self._failure_context(last_screen)}"
                )

            self.step(max(0, min(int(remaining * 1000), int(poll_interval * 1000))))
            last_screen = self.screen_text(trim=trim, unwrap=unwrap)
            if text in last_screen:
                return last_screen
            if not self.is_alive():
                raise ProcessExitedError(
                    f"Process exited before {text!r} appeared.\n{self._failure_context(last_screen)}"
                )

    def wait_for_idle(
        self,
        *,
        idle_for: float = 0.25,
        timeout: float = 5.0,
        poll_interval: float = 0.05,
    ) -> Activity:
        deadline = time.monotonic() + timeout
        idle_ms = max(0, int(idle_for * 1000))

        while True:
            activity = self.activity()
            if activity.output_quiet_ms >= idle_ms:
                return activity

            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise WaitTimeoutError(
                    "Timed out waiting for terminal output to go idle.\n"
                    f"{self._format_activity(activity)}\n"
                    f"Last screen:\n{self.screen_text(trim=True, unwrap=False)}"
                )

            self.step(max(0, min(int(remaining * 1000), int(poll_interval * 1000))))
            if not self.is_alive():
                activity = self.activity()
                if activity.output_quiet_ms >= idle_ms:
                    return activity
                raise ProcessExitedError(
                    "Process exited before terminal output went idle.\n"
                    f"{self._format_activity(activity)}\n"
                    f"Last screen:\n{self.screen_text(trim=True, unwrap=False)}"
                )

    def resize(self, cols: int, rows: int) -> None:
        self._check(_NATIVE.lib.boo_session_resize(self._handle, cols, rows))

    def is_alive(self) -> bool:
        return bool(_NATIVE.lib.boo_session_is_alive(self._handle))

    def exit_status(self) -> int | None:
        status = int(_NATIVE.lib.boo_session_exit_status(self._handle))
        return None if status < 0 else status

    def terminate(self) -> None:
        self._check(_NATIVE.lib.boo_session_terminate(self._handle))

    def close(self) -> None:
        if not self._closed:
            _NATIVE.lib.boo_session_free(self._handle)
            self._closed = True

    def __enter__(self) -> Session:
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()

    def __del__(self) -> None:
        try:
            self.close()
        except Exception:
            pass


def launch(
    command: str | Sequence[str] | None = None,
    *,
    cols: int = 80,
    rows: int = 24,
    cwd: str | os.PathLike[str] | None = None,
    env: Mapping[str, str] | None = None,
    font_size: int = 16,
    padding: int = 4,
    capture_output: bool = False,
    visible: bool = True,
    window_title: str = "boo",
) -> Session:
    return Session.launch(
        command,
        cols=cols,
        rows=rows,
        cwd=cwd,
        env=env,
        font_size=font_size,
        padding=padding,
        capture_output=capture_output,
        visible=visible,
        window_title=window_title,
    )


__all__ = [
    "Activity",
    "BooError",
    "Capture",
    "CaptureResult",
    "Idle",
    "ProcessExitedError",
    "Session",
    "WaitTimeoutError",
    "hex_bytes",
    "idle",
    "launch",
]
