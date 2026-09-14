# TODO

- [ ] Apply responsive visual design to the browser client
  - [x] Adopt Tailwind and shadcn in the Bun build with one base theme, restyle
        the existing structure without changing it, and keep accessible roles
        and names stable
  - [ ] Style the shell: sidebar, conversation header, workspace settings, and a
        drawer at phone width
  - [ ] Style the conversation: a scrolling transcript with a pinned composer,
        auto-scroll that yields to manual scrollback, and shadcn presentation
        for tool activity, plans, permissions, and questions
  - [ ] Verify the complete flow at phone and desktop viewports with focused
        accessibility and Playwright checks
