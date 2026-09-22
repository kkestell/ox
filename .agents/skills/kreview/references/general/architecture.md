# Architecture

- Name responsibilities, state owners, and dependency direction. For an
  authorized redesign, judge the proposed model rather than requiring the old
  module map to survive.
- Look for duplicate implementations, redundant wrappers, parallel
  representations, and serialization boundaries without a useful process or
  trust boundary.
- Keep dependencies few; do not recommend a library merely to abstract a
  capability used once. Integration should retire the old path.
- For claimed simplification, identify retired concepts and verify net source
  change including replacements. Update owning documents when decisions change.
