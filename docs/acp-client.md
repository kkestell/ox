# ACP Client Development Setup

Configure an ACP client to run the current Ox checkout as a local ACP agent.

## Prerequisites

Install Go 1.26.4 and an ACP client that can launch a local agent over standard
input and output. Ox also needs an OpenRouter model and credential before the
client starts it.

## Setup

1. Install the current checkout:

   ```sh
   make install
   ```

   This installs Ox at `$HOME/.local/bin/ox`. Run `make install` again after
   changing the Ox source.

2. Configure a model as described in the [settings guide](settings.md), then
   store an OpenRouter credential:

   ```sh
   ~/.local/bin/ox login
   ```

3. Follow the ACP client's instructions for registering a local agent. Configure
   it to launch the absolute path to the installed `ox` binary over standard
   input and output with no arguments. Do not use `$HOME` or `~` in the command
   path, and do not put the OpenRouter credential in the client's settings.

4. Use the ACP client to start an **Ox** session.

## Troubleshooting

If Ox does not start or the ACP connection fails, inspect the ACP client's
protocol logs and Ox's standard error output.
