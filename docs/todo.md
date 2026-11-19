# Todo

Gaps in features Ox already supports, ordered by priority.

## Approvals

- [ ] **Allow always.** Offer it when `permission_suggestions` is non-empty
  and `suppress_always_allow_rule` is false, sending the suggestions as
  `updatedPermissions`. The suggestions usually target `localSettings`, which
  writes `.claude/settings.local.json` in the workspace; decide whether that is
  acceptable before building it.

## Sessions and workspaces

- [ ] **Remove a workspace.** Needs a new method and a store delete; neither
  exists. Decide what happens to its sessions (destroy them, or refuse while
  any exist).
