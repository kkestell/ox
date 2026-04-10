"""Private session wrapper used by the Boo daemon.

This module is intentionally not re-exported from ``boo``. The supported
automation surface is the CLI, while the daemon keeps a narrow internal bridge
to the native PTY engine.
"""

from __future__ import annotations

import ctypes
import os
from collections.abc import Mapping, Sequence

from ._errors import BooError
from ._native import NATIVE, LaunchOptions


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

    def __init__(self, handle: int) -> None:
        self._handle = ctypes.c_void_p(handle)
        self._closed = False

    @classmethod
    def launch(
        cls,
        command: str | Sequence[str] | None,
        *,
        cols: int = 80,
        rows: int = 24,
        cwd: str | os.PathLike[str] | None = None,
        env: Mapping[str, str] | None = None,
    ) -> Session:
        handle = NATIVE.lib.boo_session_new()
        if not handle:
            raise BooError("boo_session_new failed")

        session = cls(handle)
        try:
            argv_array = cls._marshal_command(command)
            env_array = cls._marshal_env(env)
            options = LaunchOptions(
                size=ctypes.sizeof(LaunchOptions),
                cols=cols,
                rows=rows,
                font_size=16,
                padding=4,
                cwd=cls._encode_optional_path(cwd),
                env=env_array,
                visible=False,
                window_title=b"boo",
            )
            session._argv_array = argv_array
            session._env_array = env_array
            session._check(NATIVE.lib.boo_session_launch(session._handle, argv_array, options))
            return session
        except Exception:
            session.close()
            raise

    @staticmethod
    def _marshal_command(command: str | Sequence[str] | None) -> ctypes.Array[ctypes.c_char_p] | None:
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
    def _marshal_env(env: Mapping[str, str] | None) -> ctypes.Array[ctypes.c_char_p] | None:
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
        raw = NATIVE.lib.boo_session_last_error(self._handle)
        if not raw:
            return "boo native call failed"
        text = raw.decode("utf-8", errors="replace")
        return text or "boo native call failed"

    def _check(self, rc: int) -> None:
        if rc != 0:
            raise BooError(self._error_message())

    def _modifier_mask(self, *, ctrl: bool = False, alt: bool = False, shift: bool = False) -> int:
        mods = 0
        if shift:
            mods |= self._MOD_SHIFT
        if ctrl:
            mods |= self._MOD_CTRL
        if alt:
            mods |= self._MOD_ALT
        return mods

    def step(self, timeout_ms: int = 0) -> None:
        self._check(NATIVE.lib.boo_session_step(self._handle, int(timeout_ms)))

    def screen_text(self, *, trim: bool = True, unwrap: bool = False) -> str:
        ptr = NATIVE.lib.boo_session_snapshot_text(self._handle, trim, unwrap)
        if not ptr:
            raise BooError(self._error_message())
        try:
            return ctypes.string_at(ptr).decode("utf-8", errors="replace")
        finally:
            NATIVE.lib.boo_string_free(ptr)

    def send_text(self, text: str) -> None:
        if not isinstance(text, str):
            raise TypeError("text must be a str")
        self._check(NATIVE.lib.boo_session_send_text(self._handle, text.encode("utf-8")))

    def send_key(
        self,
        key: str,
        *,
        ctrl: bool = False,
        alt: bool = False,
        shift: bool = False,
    ) -> None:
        lookup = key.lower()
        if lookup not in self._KEYS:
            raise BooError(f"Unsupported key name: {key!r}")
        mods = self._modifier_mask(ctrl=ctrl, alt=alt, shift=shift)
        self._check(NATIVE.lib.boo_session_send_key(self._handle, self._KEYS[lookup], mods))

    def is_alive(self) -> bool:
        return bool(NATIVE.lib.boo_session_is_alive(self._handle))

    def terminate(self) -> None:
        self._check(NATIVE.lib.boo_session_terminate(self._handle))

    def close(self) -> None:
        if not self._closed:
            NATIVE.lib.boo_session_free(self._handle)
            self._closed = True

    def __del__(self) -> None:
        try:
            self.close()
        except Exception:
            pass
