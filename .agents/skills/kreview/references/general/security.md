# Security

- Identify actual trust boundaries and untrusted inputs. Tie findings to a
  realistic threat and a concrete input; do not invent a broader threat model.
- Trace permissions and workspace confinement through the operation actually
  executed, including path races, command construction, and delegated work.
- Check secret storage, diagnostic output, and tracing against their intended
  disclosure rules. Stored user content is not automatically a sanitized log.
- Resource limits should address a concrete boundary risk. An intentionally
  unbounded input or history is not automatically a defect.
