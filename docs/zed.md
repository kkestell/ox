# Zed Development Setup

Use Zed's custom External Agent support to run the current Ox checkout over ACP.

## Prerequisites

Install Go 1.26.4 and a Zed version with External Agent support. Ox also needs
an OpenRouter model and credential before Zed starts it.

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

3. In Zed, open **Agent Settings**, select **External Agents**, click **Add
   Agent**, and choose **Add Custom Agent**. Add an `agent_servers` entry like
   this to the settings file Zed opens:

   ```json
   {
     "agent_servers": {
       "Ox": {
         "type": "custom",
         "command": "/Users/you/.local/bin/ox",
         "args": []
       }
     }
   }
   ```

   Replace `/Users/you/.local/bin/ox` with the absolute path to your installed
   binary. Do not use `$HOME` or `~` in this value, and do not put the
   OpenRouter credential in Zed's settings.

4. Open Zed's Agent Panel. Use the agent selector or new-thread menu to start an
   **Ox** thread.

## Troubleshooting

If Ox does not start or the ACP connection fails, run `dev::OpenAcpLogs` from
Zed's Command Palette. The ACP logs show protocol messages and Ox's stderr
output.
