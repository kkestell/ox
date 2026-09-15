# TODO

- [ ] Apply responsive visual design to the browser client
  - [x] Replace Tailwind and the vendored shadcn components with one
        hand-written stylesheet and the platform's own controls, without
        changing structure or accessible names
  - [x] Style the shell: sidebar, conversation header, workspace settings, and a
        drawer at phone width
  - [ ] Style the conversation: a scrolling transcript with a pinned composer,
        auto-scroll that yields to manual scrollback, and a settled presentation
        for tool activity, plans, permissions, and questions
  - [ ] Verify the complete flow at phone and desktop viewports with focused
        accessibility and Playwright checks
