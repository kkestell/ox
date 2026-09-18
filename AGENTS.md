# Source Map

- `src/main.rs`: ACP agent entry point; registers initialization, session, prompt, and cancellation handlers over stdio.
- `src/sessions.rs`: SQLite-backed session, workspace, and transcript storage; includes unit tests for persistence behavior.
