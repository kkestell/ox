"""Boo is a CLI-first terminal automation tool.

The supported automation surface is the ``boo`` command. The daemon keeps its
native session wrapper in private modules so external callers do not couple to
an in-process scripting API.
"""

from __future__ import annotations

from ._errors import BooError, ProcessExitedError, WaitTimeoutError

__all__ = [
    "BooError",
    "ProcessExitedError",
    "WaitTimeoutError",
]
