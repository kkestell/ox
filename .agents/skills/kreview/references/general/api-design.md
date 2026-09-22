# API design

- Read real call sites. Keep shared surface small, names clear, and argument
  meaning apparent. Check whether wrappers and conversions improve the actual
  interface or only spread complexity.
- Abstractions need real uses that make them clearer than a direct interface.
- Check protocol encoding where field presence, defaults, or optional values
  change observable behavior. Extension or context storage also has an
  ownership and lifetime cost.
