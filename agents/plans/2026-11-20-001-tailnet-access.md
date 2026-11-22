# Serve the web UI on the user's tailnet

## Goal

Enable tailnet access by adding `"tailscale_ip": "100.101.102.103"` to Ox's
configuration with this machine's Tailscale IPv4 address. At startup, Ox serves
the web UI there on port 17840 alongside its existing listener and prints the URL, for example
`http://100.101.102.103:17840`. Ox does not discover the address or invoke the
Tailscale CLI.

## Related code

- `internal/config/config.go` loads and validates the JSON file.
- `cmd/ox/main.go` starts the relay. It rejects wildcard binds, checks the
  host on `/events` and `/rpc`, and checks the origin on `/rpc`.
- `cmd/ox/web/dev.mjs` starts the relay at `localhost:8081` behind Vite on
  port 8080. The relay also serves the embedded UI.
- `cmd/ox/web/e2e/services.ts` gives the daemon a temporary configuration and
  the relay its own local address and socket.

## Decisions

- Tailnet access is off when `tailscale_ip` is omitted or empty. A nonempty
  value must be an IPv4 address within `100.64.0.0/10`. The user supplies
  this machine's address; Ox requires no Tailscale CLI.
- Ox binds directly to the configured address on dedicated port 17840,
  independent of the local listener's port and `-addr` override. Ox rejects
  an invalid address or reports a failed bind and exits before serving. It
  prints the HTTP URL when both listeners have bound. Under `make dev`, the
  tailnet URL also uses port 17840 and serves the embedded build from startup.
  The local Vite URL still live-reloads.
- Both listeners use one relay and its existing cross-origin protection.
  The configured address joins the relay's allowed hosts from the same
  value used to bind the listener. A client that can reach the tailnet
  port can use the same session and workspace controls as a local client.
  Tailscale policy controls access over the tailnet. Ox adds no login.

## Implementation plan

1. In `internal/config/config.go`, add the optional string `tailscale_ip`,
   defaulting to empty. Validate nonempty values with `netip.ParseAddr` and
   accept only an IPv4 address within `100.64.0.0/10`. Keep strict JSON
   decoding and the existing configuration requirements.
2. In `cmd/ox/main.go`, load the shared configuration via `paths.Config()` or
   the same `-config` override as `oxd`. Keep the existing `-addr` default
   and override. Use the validated `tailscale_ip` for the optional tailnet
   listener on port 17840; do not add CLI discovery. Bind the local and
   optional tailnet listeners before serving with the shared handler.
   Populate the relay's allowlist from their configured hostnames and exit
   on a serving error.
3. In `cmd/ox/web/e2e/services.ts`, pass the test's temporary configuration
   to `ox` as well, so the test runner uses its own network settings.
4. Apply the requirement change below. In `docs/protocol.md`, document the
   optional setting. In `docs/architecture.md`, document the shared relay,
   configured tailnet listener, and embedded UI served by
   the tailnet listener under `make dev`.

## Test plan

- In `internal/config/config_test.go`, load a valid configuration with a
  Tailscale IPv4 address and verify the address is loaded. Cover an omitted
  or empty address, malformed addresses, IPv6, and IPv4 outside Tailscale's
  range.
- In `cmd/ox`, exercise optional listener startup and bind failure with
  supplied addresses. Verify the tailnet listener uses port 17840 regardless
  of the local `-addr` port. Extend the existing relay tests for a matching
  origin and a foreign origin on the tailnet host.
- Run `make test` and `make e2e`. In headless Chrome on the `make dev` stack,
  use temporary XDG directories, the fake harness, and `tailscale_ip` set to
  this machine's Tailscale IPv4 address. Open the printed URL, send a prompt,
  observe the echo, and verify the local Vite URL sees the session.

## Requirement changes

- Replace **Local access only.** Only pages served from this machine (or from the host `ox` was told to serve) can reach `oxd`.

  **Web access.** The web relay accepts requests addressed to Ox's local or configured hosts. When the user configures `tailscale_ip`, Ox also serves the web UI on that Tailscale address at port 17840, independently of the local listener's port, and prints that URL at startup.
