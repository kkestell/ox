let sequence = 0;

// Browser commands only need an identifier unique to this page. Avoid Web
// Crypto here because randomUUID is unavailable when the client is served over
// plain HTTP from a LAN or Tailscale address.
export function newRequestID(): string {
  sequence += 1;
  return `browser-${Date.now().toString(36)}-${sequence.toString(36)}`;
}
