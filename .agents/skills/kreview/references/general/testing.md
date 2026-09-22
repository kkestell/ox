# Testing

- Check important retained and changed behavior at a stable boundary,
  including failure paths and interactions with a concrete regression risk.
- Assertions should distinguish correct behavior from the reported defect and
  survive implementation changes. Tests for retired mechanisms may also retire.
- Prefer existing deterministic test facilities. Do not demand a separate test
  for every error message, comparison, or internal helper. External service
  checks require appropriate authorization.
