"""Shared Boo exception types.

The package is CLI-first now, but the daemon and client still need a small
internal exception vocabulary so failures propagate cleanly across layers.
"""

from __future__ import annotations


class BooError(RuntimeError):
    pass


class ProcessExitedError(BooError):
    pass


class WaitTimeoutError(BooError):
    pass
