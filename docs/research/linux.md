# Selected Linux lessons from Hypercube

Extracted on 2026-11-12 from Hypercube's
[Linux support findings](https://github.com/kkestell/hypercube/blob/c094fa68f409005f1ba4155fc7aa84fde5ce5c23/.agents/research/linux.md).
The original observations date to 2026-11-06 and 2026-11-07. These are historical
findings, not Linux verification of Ox. See [import provenance](README.md).

Hypercube used Go and Electron; Ox uses Rust and Tauri. The old packaging,
Chromium-sandbox, Electron-notification, and menu-accelerator instructions are not
included because they do not establish requirements or behavior for Ox's stack.

## Service process ownership

systemd's default `KillMode=control-group` stops all processes in the service's
control group. Starting a child with `setsid` does not move it out of that group.
`KillMode=process` signals only the main process; in Hypercube's Debian test, that
left a child running across a user-service restart.

That was relevant to Hypercube's holders, which kept agents alive across backend
restarts. It is not a recommendation to set `KillMode=process` for Ox: Ox's daemon
owns its agents and deliberately kills them on shutdown, preserving saved sessions
for later resume. If Ox gains a systemd service, choose its process policy from
that lifecycle rather than inheriting Hypercube's policy.

## Launch environment

- systemd user services set `HOME`, `USER`, `LOGNAME`, and `SHELL`, but do not
  automatically load the user's shell configuration. Hypercube launched through an
  interactive login shell to obtain the terminal's PATH and credentials. That
  approach can emit bash job-control warnings without a terminal; it is a recorded
  Hypercube mechanism, not a prescribed Ox launch strategy.
- The tested CLI installers added PATH entries to `~/.profile` or `~/.bashrc`.
  Discovery from a desktop app or service must account for its actual launch
  environment rather than assume it matches a terminal.
- A systemd user manager may lack `DISPLAY` and `WAYLAND_DISPLAY`. GNOME and KDE
  imported the graphical environment; minimal compositors might not. Do not assume
  a daemon can launch graphical applications merely because the desktop client can.
- Hypercube's `/bin/zsh` fallback failed on Linux machines without zsh. Do not
  assume the developer's macOS shell exists on Linux.

## Harness observations

In a Debian 13 container on 2026-11-07, Hypercube discovered models and ran a
shell-tool session with Claude Code 2.1.319, Codex 0.166.0, OpenCode 2, and Pi 1.5.0,
installed using their vendors' Linux installers.

- Claude Code read Linux credentials from `~/.claude/.credentials.json`, versus
  the macOS Keychain.
- OpenCode 2 stored logins in the `credential` table of
  `~/.local/share/opencode/opencode.db`, not `auth.json`.
- Pi required Node.js 22.19 or later; Debian 13 shipped Node 20.
- Codex warned when `bubblewrap` was absent from PATH and used its bundled copy.

These version-specific observations do not verify current installers, auth formats,
or Ox adapters. See the [harness surveys](README.md#harnesses) for more context.

## Portability and testing

- GNU `cp` has no macOS `cp -c` option. Hypercube's worktree-copy path failed
  consistently on Linux because it assumed that option existed.
- Faster/different process scheduling exposed races also present on macOS: a
  harness could exit before the first stdin write, masking its own startup error
  with a broken-pipe error. Another test observed idle before final transcript
  content. Preserve the real process error and wait for the event whose state a
  test asserts, not an adjacent lifecycle event.
- Docker tests without an init process left killed orphans as zombies, making
  `kill(pid, 0)` look as though they were still running. Hypercube's affected tests
  passed with `docker run --init`.
- Shell semantics matter: Debian's dash made a bare `exit` in a HUP trap return
  the killed `sleep` command's status in one fixture. Test fixtures must specify
  the intended exit status rather than rely on a developer's shell behavior.
- A privileged Debian 13 container booted with `/sbin/init` exercised actual
  systemd user-service restarts. Ordinary containers did not establish those
  service-lifecycle guarantees.
- Containers share the host kernel. They cannot independently validate a different
  distribution's kernel security policy. Hypercube used a real Ubuntu CI kernel
  for its Ubuntu AppArmor checks; its Electron sandbox findings do not establish
  Tauri/WebKit behavior.
- Docker on the development Mac ran ARM64 Linux natively. Emulated AMD64 desktop
  testing was too slow in that investigation; AMD64 checks ran in CI instead.
