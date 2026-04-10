"""ctypes bindings for the private headless Boo native library."""

from __future__ import annotations

import ctypes
import os
from ctypes.util import find_library
from pathlib import Path

from ._errors import BooError


class LaunchOptions(ctypes.Structure):
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


class NativeBindings:
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
            "Could not find libboo_tester; build the project to place it next to boo/_native.py, "
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
            ctypes.POINTER(LaunchOptions),
        ]
        self.lib.boo_session_launch.restype = ctypes.c_int

        self.lib.boo_session_step.argtypes = [ctypes.c_void_p, ctypes.c_int]
        self.lib.boo_session_step.restype = ctypes.c_int

        self.lib.boo_session_send_text.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
        self.lib.boo_session_send_text.restype = ctypes.c_int

        self.lib.boo_session_send_key.argtypes = [
            ctypes.c_void_p,
            ctypes.c_int,
            ctypes.c_uint16,
        ]
        self.lib.boo_session_send_key.restype = ctypes.c_int

        self.lib.boo_session_snapshot_text.argtypes = [
            ctypes.c_void_p,
            ctypes.c_bool,
            ctypes.c_bool,
        ]
        self.lib.boo_session_snapshot_text.restype = ctypes.c_void_p

        self.lib.boo_string_free.argtypes = [ctypes.c_void_p]

        self.lib.boo_session_is_alive.argtypes = [ctypes.c_void_p]
        self.lib.boo_session_is_alive.restype = ctypes.c_bool

        self.lib.boo_session_terminate.argtypes = [ctypes.c_void_p]
        self.lib.boo_session_terminate.restype = ctypes.c_int


NATIVE = NativeBindings()
