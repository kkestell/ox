# Git Worktrees

Ox accepts a Git worktree as an ordinary session workspace. Create the worktree
yourself, then choose its absolute path as the project root in your ACP client:

```sh
git worktree add -b my-feature /absolute/path/to/my-feature
```

The client passes that path as the session `cwd`. Ox uses it for file tools,
workspace instructions, and memory. Closing or deleting the session never
removes the worktree, its branch, or its dirty and untracked files.

Remove the worktree explicitly when you are finished:

```sh
git worktree remove /absolute/path/to/my-feature
git branch -d my-feature
```

File tools are confined to the selected root. This is filesystem confinement,
not a system sandbox: approved shell commands, language servers, and MCP servers
retain the user's host privileges. Sibling worktrees also share repository
metadata, including refs and object storage.
