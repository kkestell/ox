# Zed Setup

Use Zed's custom External Agent support to run Ox over ACP.

## Prerequisites

Install Ox as described in the [installation guide](installation.md), and use a
Zed version with External Agent support. Ox also needs an OpenRouter model and
credential before Zed starts it.

## Setup

1. Configure a model as described in the [settings guide](settings.md), then
   store an OpenRouter credential:

   ```sh
   ox login
   ```

2. In Zed, open **Settings → AI → Configure External Agent → Add Agent**, then
   set:

   - **Agent Name:** `Ox`
   - **Command:** `/Users/you/.local/bin/ox`

   Replace `/Users/you/.local/bin/ox` with the absolute path to your installed
   binary. Do not use `$HOME` or `~` in this value, and do not put the
   OpenRouter credential in Zed's settings. To use a credential file instead of
   the keyring, pass `--credential-file` and its absolute path to that command;
   make that file readable only by your user.

3. Open the project directory that Ox should use as its workspace, then open
   Zed's Agent Panel. Use the agent selector or new-thread menu to start an
   **Ox** thread. To work on an independent branch, prepare a Git worktree and
   open it as the project directory.

## Troubleshooting

If Ox does not start or the ACP connection fails, run `dev::OpenAcpLogs` from
Zed's Command Palette. The ACP logs show protocol messages and Ox's stderr
output.
