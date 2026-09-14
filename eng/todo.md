# TODO

- [ ] Restructure the browser client around a workspace and conversation sidebar
  - [ ] Carry a bounded recent-conversation list for every registered workspace
        in the host snapshot, so cross-workspace navigation does not require
        selecting a workspace first
  - [ ] Move workspace registration, selection, and conversation history into
        one sidebar navigation region, leave the selected conversation as the
        only primary content, add the minimal layout stylesheet that places the
        sidebar beside it, and replace the pre-CSS invariant in
        `eng/client-architecture.md`
  - [ ] Move authentication, MCP servers, and support details out of the primary
        column into a workspace settings surface reached from the sidebar
- [ ] Apply responsive visual design to the browser client
  - [ ] Adopt Tailwind and shadcn in the Bun build with one base theme, restyle
        the existing structure without changing it, and keep accessible roles
        and names stable
  - [ ] Style the shell: sidebar, conversation header, workspace settings, and a
        drawer at phone width
  - [ ] Style the conversation: a scrolling transcript with a pinned composer,
        auto-scroll that yields to manual scrollback, and shadcn presentation
        for tool activity, plans, permissions, and questions
  - [ ] Verify the complete flow at phone and desktop viewports with focused
        accessibility and Playwright checks
