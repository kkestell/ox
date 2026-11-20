# Serve the web UI on the user's tailnet

## Goal

Enable tailnet access with `{"tailscale":true}` in Ox's configuration.
At startup, Ox looks up its Tailscale IPv4 address, serves the web UI there
alongside its existing listener, and prints the URL. This Mac's installed
Tailscale client reports an IPv4 address that a local TCP socket can bind.

## Related code

- `internal/config/config.go` loads and validates the JSON file. The built-in
  catalog currently applies only when the file is missing.
- `cmd/ox/main.go` starts the relay. It rejects wildcard binds, checks the
  host on `/events` and `/rpc`, and checks the origin on `/rpc`.
- `cmd/ox/web/dev.mjs` starts the relay at `localhost:8081` behind Vite on
  port 8080. The relay also serves the embedded UI.
- `cmd/ox/web/e2e/services.ts` gives the daemon a temporary configuration and
  the relay its own local address and socket.

## Decisions

- The flag is off by default. When enabled, `ox` gets the local address
  from the installed client's documented `tailscale ip -4` command. Resolve
  `tailscale` on PATH first. On macOS, use the bundled CLI at
  `/Applications/Tailscale.app/Contents/MacOS/Tailscale` if no CLI is on PATH.
  This Mac uses the bundled path. Set `TAILSCALE_BE_CLI=1` on the invocation
  so the app runs in CLI mode. [Tailscale CLI documentation](https://tailscale.com/docs/reference/tailscale-cli?tab=macos)
- Ox runs the command once with a bounded startup wait, parses stdout as an
  IPv4 address in Tailscale's range, and binds directly to it using the
  local listener's port. Ox reports a failed lookup or bind and exits before
  serving. It prints the HTTP URL when both listeners have bound. Under
  `make dev`, the tailnet URL uses the relay port (8081) and serves the
  embedded build from startup. The local Vite URL still live-reloads.
- Omitting both `harnesses` and `defaults` uses the built-in catalog so the
  switch works in a one-setting file. When either catalog key is present,
  the file must still supply a valid complete catalog and defaults.
- Both listeners use one relay and its existing cross-origin protection.
  The discovered address joins the relay's allowed hosts from the same
  value used to bind the listener. A client that can reach the tailnet
  port can use the same session and workspace controls as a local client.
  Tailscale policy controls access over the tailnet. Ox adds no login.

## Implementation plan

1. In `internal/config/config.go`, add the optional boolean `tailscale`,
   defaulting to false. Keep strict JSON decoding. Track whether the catalog
   keys were supplied so that the loader uses the built-in catalog only
   when both are omitted. Use the existing catalog validation for both
   sources.
2. In `cmd/ox/main.go`, load the shared configuration via `paths.Config()` or
   the same `-config` override as `oxd`. Keep the existing `-addr` default
   and override. Use one startup function in `cmd/ox` for resolving and
   running the Tailscale CLI. Parse its stdout with `netip.ParseAddr` and
   accept only an address within `100.64.0.0/10`. Include the command's
   error or stderr when available. Do not run the command unless enabled.
   Bind the local and optional tailnet listeners before serving with the
   shared handler. Populate the relay's allowlist from their configured
   hostnames and exit on a serving error.
3. In `cmd/ox/web/e2e/services.ts`, pass the test's temporary configuration
   to `ox` as well, so the test runner uses its own network settings.
4. Apply the approved requirements below. In `docs/protocol.md`, show the
   one-setting config and CLI requirement. In `docs/architecture.md`,
   document the shared relay, startup discovery, and embedded UI served by
   the tailnet listener under `make dev`.

## Test plan

- In `internal/config/config_test.go`, load the one-setting file and verify
  it enables Tailscale with the built-in catalog. Keep coverage of incomplete
  custom catalogs.
- In `cmd/ox`, exercise discovery with a small stand-in CLI that returns a
  tailnet address and an unusable result. Extend the existing relay tests
  for a matching origin and a foreign origin on the tailnet host.
- Run `make test` and `make e2e`. In headless Chrome on the `make dev` stack,
  use temporary XDG directories, the fake harness, and `"tailscale":true`.
  Open the printed URL, send a prompt, observe the echo, and verify the
  local Vite URL sees the session.

## Requirement changes

- Replace **Local access only.** Only pages served from this machine (or from the host `ox` was told to serve) can reach `oxd`.

  **Web access.** The web relay accepts requests addressed to Ox's local or configured hosts. When Tailscale access is enabled, Ox also serves the web UI on its detected Tailscale address and prints that URL at startup.

- Replace **Built-in catalog.** Without a configuration file, Ox offers Claude Opus, Sonnet, and Haiku 5.5 with their effort levels and the manual, auto, acceptEdits, and plan modes.

  **Built-in catalog.** When the user does not configure a catalog or its defaults, Ox offers Claude Opus, Sonnet, and Haiku 5.5 with their effort levels and the manual, auto, acceptEdits, and plan modes.
