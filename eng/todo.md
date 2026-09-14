# TODO

- [ ] Add browser-managed multi-workspace operation
  - [x] Persist a registry of validated server-local workspace roots and add
        browser flows to register, select, and remove them without exposing a
        general filesystem API
  - [ ] Run and route one independently supervised Ox process per active
        workspace, and prove concurrent turns and failure isolation across two
        roots
  - [ ] Complete multi-workspace restart, interaction-routing, and browser
        coverage, then run the milestone completeness and simplification review
- [ ] Apply responsive visual design to the feature-complete browser client
  - [ ] Add the first CSS after the functional gate, preserving semantic control
        behavior, and verify the complete flow at phone and desktop viewports
        with focused accessibility and Playwright checks
