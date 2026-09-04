# 2026-08-17-010. Workspace confinement

## Goal

`session/new` canonicalizes `cwd` with `canonicalDirectory` in
`internal/agent/session.go` and keeps the result as a string on the session.
Nothing else in Ox has an opinion about a path. The next items add tools that
take paths from the model, and without one rule in one place each of them
decides for itself what a path means and whether it is allowed. That is how an
agent ends up reading `../../.ssh/id_ed25519` because one tool joined a path
that another tool checked, or writing through a symlink that leaves the tree
because the check ran before the open and the tree changed in between.

There is a second, smaller gap in what is already there. `canonicalDirectory`
accepts any directory it can `Stat`, so a working directory Ox cannot list
creates a session successfully and then fails every tool call that touches it.

## Desired outcome

A session owns a workspace: one canonical directory, and one function that turns
a path from the model or the client into a location inside it.

A path that resolves outside the tree is refused, and the refusal names the path
as the caller wrote it. That covers an absolute path somewhere else, a `..`
climb, a path whose route passes through a symlink that leaves the tree, and a
symlink at the end of a path whose target is outside.

A path inside the tree comes back as a value carrying both spellings a caller
needs — the workspace-relative one to show a person and the model, and the
absolute one ACP requires in tool-call locations and in `fs/read_text_file` —
and it opens its file through the same kernel-held root that enforced the
boundary, so the check and the operation cannot disagree about which file they
mean. Opening anything that is not a regular file is refused rather than
attempted, so a device node or a named pipe inside the workspace cannot stall a
turn.

The spelling a client actually sends resolves. Someone whose project is reached
through a symlink — `~/proj` pointing at `/mnt/data/proj`, or anything under
macOS's `/var`, which is itself a symlink to `/private/var` — gets a session
rooted at the real directory, and an absolute path the client sends later in the
original spelling still lands inside that root instead of reading as an escape.

`session/new` refuses a working directory Ox cannot list with `-32602`, rather
than leaving it to surface as a puzzling tool failure later.

Nothing on the ACP surface changes. No method is added, no capability is
advertised, and no request or response gains a field.

## Summary of approach

A new package, `internal/workspace`, owns every path decision in Ox. It is one
file, because it is one rule.

`Canonical(dir)` is the boundary check `session/new` runs on `cwd`: resolve
symlinks, require a directory, and require that Ox can list it. `New(root)`
wraps a canonical root in a `Workspace` value, and panics unless the root is
absolute — past the boundary, a workspace rooted at a relative path is a bug,
and every confinement decision below depends on the root being real.

`Resolve(path)` is the single rule. A relative path is joined onto the root; an
absolute path is taken as it stands. Either way the result's existing prefix is
resolved through symlinks, the missing tail is reattached, and the outcome is
made relative to the canonical root. A relative name that climbs out is refused
as outside the workspace; anything that survives is a `Path`. One code path
serves both spellings, so there is no separate lexical rule for relative paths
to disagree with.

Placing the path comes before reporting anything about it. Resolution reattaches
the missing tail whether or not it hit a failure along the way, and `Resolve`
tests that best resolution against the root first. A path that leaves the tree
is refused as outside the workspace even when Ox could not reach it, so a `..`
climb into a directory Ox cannot traverse does not answer with the host's
permissions and tell the model what lives out there. A path inside the tree that
Ox cannot reach still reports the reason, because that is a reason the model can
act on.

Resolving before relativizing is what makes a client's own spelling work.
`filepath.Rel` is lexical: with the root canonicalized to
`/private/var/.../project`, a client's `/var/.../project/src/main.go`
relativizes to a seven-level `..` climb and reads as an escape. Resolving the
path first turns it into `src/main.go`. It also normalizes a route through a
symlink inside the tree, so two spellings of one file produce one
workspace-relative name. Resolving cannot let anything in: a path that resolves
outside the root is refused, and a path that resolves inside is inside.

`Path` carries the canonical root and the workspace-relative name. `String()` is
that name with forward slashes, which is what a tool shows and what an error
quotes. `Absolute()` joins it back onto the root, which is what ACP requires in
a `ToolCallLocation` and in an `fs/*` request to a client. `Open()` opens the
root with `os.OpenRoot`, opens the name inside it, and rejects anything that is
not a regular file.

`os.Root` is the actual boundary, and the reason to open through it rather than
to check a path and then use it. Every component of the name is resolved by the
kernel against the held directory handle, so a `..` that climbs out or a symlink
whose target is outside is refused by the same handle that performs the
operation. A resolve-then-open design leaves a window in which the tree changes
between the two; there is no such window here. The resolution `Resolve` does is
only how the name to try is chosen; `os.Root` decides whether it is allowed. The
handle can be closed as soon as the file is open, because the returned
descriptor stands on its own.

`os.Root` reports an escape as an `*fs.PathError` whose inner error is
`errPathEscapes`, "path escapes from parent". That error is unexported and there
is no sentinel to compare against, so the message is matched, and the match has
one home. An escape becomes "outside the workspace", a non-regular file becomes
"not a regular file", and anything else becomes "cannot access" carrying the
reason. Reasons are unwrapped to the bare cause before they are quoted: an
`*fs.PathError` from a root names the path relative to that root, but one from
resolving symlinks names an absolute host path, and a model-facing message
should not carry it.

The open passes `syscall.O_NONBLOCK`. Opening a named pipe with no writer blocks
in the kernel until one arrives, which would hang a turn on nothing but the
model naming a FIFO that happens to sit in the tree; with the flag the open
returns, and the regular-file check refuses it. The constant exists on every
platform Go supports, so this needs no build tags, and it is a no-op for the
regular files every real read hits.

The session holds a `workspace.Workspace` in place of its `cwd` string.
`NewSession` canonicalizes, builds the workspace, and passes `Root()` to
`config.Resolve`, which keeps taking a directory and learns nothing about
workspaces. `canonicalDirectory` is deleted.

One session has exactly one root, and it is both readable and writable. Alpha
and Theta also carry readable-only roots so a model can page back through shell
output that spilled to a file outside the tree; Ox has no shell tool and no
spill directory, so a second kind of root would be machinery for a case that
does not exist.

## Related code

- `~/src/references/repos/personal/alpha/runtime/internal/workspace/workspace.go:36-65`
  — `Canonical`: absolute, `EvalSymlinks`, `Stat` for a directory, then `Open`
  plus `Readdirnames(1)` with `io.EOF` tolerated, which is the listability check
  Ox is missing. `:228-238` is `confine`, which locates a root and opens it with
  `os.OpenRoot`; `:240-254` is `locate`, the lexical split this plan replaces
  with one resolving rule; `:256-258` is `escapes`, taken as is; `:260-278` is
  the `accessError`/`writeError`/`isEscape` trio and the "escapes from parent"
  match. `:319-344` is `resolveExisting`, which walks up to the deepest existing
  ancestor, resolves it, and reattaches the missing tail — the piece this plan
  promotes from a write-path detail to the whole of path resolution. `:183-207`
  is `Key`, which uses it to canonicalize a path for identity but only after the
  lexical test has already refused a client's uncanonical spelling. `:78-181`
  are `ReadFile`, `WriteFile`, and `Edit`, whose confinement this plan lands and
  whose paging, atomic replace, and read-evidence guards belong to the file-tool
  items. `platform.go:11-26` is `openRegularFile`, the `O_NONBLOCK` open plus
  regular-file check.
- `~/src/references/repos/personal/theta/internal/tools/workspace.go:6-30` — the
  clearest statement of why confinement runs through `os.Root`: one handle for
  the check and the operation, closing the window a resolve-then-open design
  leaves. `:185-217` is the same `confine`/`locate` pair as Alpha's, confirming
  the lexical-only treatment of absolute paths is the shared inheritance and not
  a local slip. `:87-101` is `WorkspacePrompt`, which tells the model the root
  and how paths resolve against it; recorded as belonging with the tools, since
  Ox sends no system prompt yet.
- `~/src/references/repos/personal/beta/src/workspace.rs:59-119` — `read_path`
  and `write_path`: canonicalize, then compare prefixes, then operate on the
  canonical path. Recorded as the option not taken. It needs two rules because
  the check is separate from the operation, one for an existing leaf and one for
  a fresh file confined through its parent, and it leaves the window between the
  check and the open that `os.Root` closes. `:1-13` and `:121-140` are its prose
  on the rule and on relativizing for reports.
- `~/src/references/repos/personal/gamma/internal/tools/workspace.go:38-93` —
  `confine`, `ReadPath`, and `WritePath`: string-prefix containment plus
  `EvalSymlinks`, with a not-yet-existing path passed through unresolved. The
  same option, in Go, and the same window.
- `~/src/references/repos/third-party/coding-agents/codex/codex-rs/sandboxing`,
  `linux-sandbox`, `windows-sandbox-rs`, and `docs/sandbox.md` — kernel
  sandboxing of the whole child process, per platform. Recorded as the option
  not taken and not a substitute: it confines a spawned command, not the agent's
  own reads and writes, and it is a subsystem to consult about rather than a
  detail of this item.
- `os` package, Go 1.26.4: `Root`'s documentation states that its methods follow
  symlinks but refuse a target outside the root, and lists what it does not
  promise — filesystem boundaries, bind mounts, `/proc`, and device files.
  `os/file.go:421` is `errPathEscapes` and the message the mapping matches.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json`
  — `ToolCallLocation.path` is "The absolute file path being accessed or
  modified", and `fs/read_text_file` and `fs/write_text_file` both take
  "Absolute path to the file". This is what `Path.Absolute()` exists for, and it
  is why confinement has to produce a path the client can use as well as one a
  person can read.
- `internal/agent/session.go:173-186` — `canonicalDirectory`, which this plan
  replaces; `:23-31` and `:59` are the session's `cwd` field and the one place
  it is set; `:47-54` is the boundary sequence `session/new` runs, canonicalize
  then resolve configuration.
- `internal/acp/validate.go:57-74` — `NewSessionRequest.Validate`, which already
  requires `cwd` and requires it to be absolute, and already refuses
  `additionalDirectories`. It keeps both rules; the workspace package assumes
  them rather than repeating them.
- `internal/config/config.go:63-65,97-107` — `workspacePath` and `Resolve`, the
  existing consumers of the canonical directory, unchanged beyond where the
  string comes from.
- `internal/e2e/session_test.go:51-105` — the table of `session/new` refusals,
  which the unlistable working directory joins as its own test because it needs
  to restore the directory's mode before the scratch tree is removed.

## Current state

- Relevant existing behavior: `session/new` validates the request in
  `internal/acp`, canonicalizes `cwd` with `EvalSymlinks` and a `Stat`, resolves
  configuration against the canonical directory, and stores the directory as
  `session.cwd`. `cwd` is read once more, to log it. No other code in Ox touches
  a filesystem path outside `internal/config`'s own two files and
  `internal/credentials`.
- Existing patterns to follow: a package owns one concern and states it in a
  package comment, as `internal/config` does. Protocol-shape validation lives in
  `internal/acp` and the handler maps it to `-32602`. Values are preferred over
  pointers, and a type stays concrete until a second implementation exists.
- Constraints from the current implementation: there is no tool, no system
  prompt, and no `fs/*` delegation, so a confined path has no consumer yet
  beyond the session that owns the root. The end-to-end harness can only observe
  `session/new`.
- The harness passes `child.cwd`, the scratch directory as `t.TempDir()`
  reported it, which on macOS is under `/var` and therefore not canonical. Ox
  canonicalizes it to `/private/var/...`. The existing assertions survive that
  only because the canonical form contains the original as a substring. This is
  the uncanonical-spelling case in miniature, and it is the reason not to
  canonicalize `child.cwd` in the harness: the harness should keep sending what
  a client sends.

## Structural considerations

- **Hierarchy:** `internal/workspace` depends on the standard library and
  nothing else in Ox. `internal/agent` depends on it, as the tool packages will.
  Nothing depends on `internal/agent`, so no ACP or session concept can leak
  into the path rule.
- **Abstraction:** the package answers one question — where in this tree does
  this path point, and may it be touched — and answers it once. `internal/agent`
  keeps deciding which ACP error code a refusal becomes, and `internal/config`
  keeps taking a plain directory. Deciding what a read means, how much of a file
  to return, or when to ask permission stays out.
- **Modularization:** one package and one file. The pieces are a boundary check,
  a resolution rule, and a confined open, and they exist only in terms of each
  other; splitting them across files would separate a rule from its enforcement.
  The file-operation methods the next items add belong here for the same reason
  — they are the operations the held root exists to perform.
- **Encapsulation:** `Path`'s fields are unexported and only `Resolve` mints
  one, so a path that no rule has seen cannot be passed to an operation.
  `os.Root` never leaves the package, so the kernel handle cannot outlive the
  operation it guards or be reused for a name that was never resolved. The zero
  `Workspace` is not usable, which is why `New` asserts instead of tolerating
  it.
- **Testability:** every rule is observable at the package's public interface
  with real directories, symlinks, and a FIFO under `t.TempDir()`. The boundary
  check is additionally observable over stdio, because it decides whether
  `session/new` succeeds. Confinement itself gets its end-to-end coverage with
  the first tool that takes a path, because there is nothing to drive it through
  the protocol until then; keeping this item to the rule is what makes that
  acceptable rather than a gap.

## Refactoring

- Replace the session's `cwd string` with `workspace workspace.Workspace` and
  delete `canonicalDirectory` from `internal/agent/session.go`. The session
  should own the workspace rather than a string that every future tool would
  have to turn into one, and one place should decide how a session's workspace
  is built.

## Test plan

- **Key behaviors to verify:**
  - `Canonical` resolves a symlinked directory to its target, and refuses a path
    that does not exist, a file, and a directory it cannot list.
  - `New` panics on a relative root.
  - `Resolve` accepts a relative path, a relative path with `.` and interior
    `..` that stays inside, an absolute path inside the root, and the root
    itself, and reports each one's workspace-relative name with forward slashes.
  - `Resolve` accepts an absolute path spelled through a symlinked ancestor of
    the root and reports the same name as the canonical spelling. This is the
    case the references get wrong.
  - `Resolve` normalizes a route through a symlink inside the tree, so a path
    through the link and a path through the real directory produce one name.
  - `Resolve` refuses `..`, a deeper climb that lands back outside, an absolute
    path elsewhere on the filesystem, and a path whose interior component is a
    symlink out of the tree. Each refusal says the path is outside the workspace
    and quotes the path as it was given.
  - `Resolve` accepts a path whose tail does not exist yet, since a write needs
    one, and refuses one whose missing tail sits under a directory outside the
    tree.
  - `Resolve` refuses a path outside the tree that Ox cannot traverse as outside
    the workspace, spelled both relatively and absolutely, and still reports the
    reason for a path inside the tree that Ox cannot traverse.
  - `Absolute` is the root joined with the name, for every spelling that
    resolves.
  - `Open` reads a file addressed relatively, absolutely, and through a symlink
    inside the tree.
  - `Open` refuses a symlink whose target is outside the tree as outside the
    workspace, including one created after `Resolve` returned, which is the case
    the held root exists for.
  - `Open` refuses a directory and a named pipe as not a regular file, and the
    FIFO case returns rather than blocking.
  - `Open` reports a missing file as an access failure naming the reason, and
    neither that message nor a refusal carries an absolute host path.
  - `session/new` refuses a working directory Ox cannot list with `-32602`, and
    the existing refusals and successes are unchanged.
  - A session created through a symlinked working directory resolves its
    workspace configuration from the real directory.
- **Test levels:** the rule is unit-tested in `internal/workspace` against real
  trees under `t.TempDir()`, because a symlink, a FIFO, and a mode change are
  what the rule is about and they cannot be faked at a higher level. The FIFO
  test is a separate `//go:build unix` file, since `syscall.Mkfifo` is not
  portable. The working-directory refusal is end to end, because it is a
  protocol boundary a client sees.
- **Edge cases and failure modes:** a dangling symlink, which fails to resolve
  and is refused as an access failure rather than followed; an empty path, which
  is the root; two spellings of one file, which produce one name; a workspace
  whose root is removed after the session is created, which fails at the
  operation with a plain access error. The unlistable-directory tests restore
  the mode in `t.Cleanup` so the scratch tree can be removed, and skip when the
  tests run as root, where the mode would not be enforced.
- **What not to test:** that `os.Root` is correct, which is the standard
  library's own coverage; the limits `os.Root` documents and this plan inherits
  — bind mounts, `/proc`, device files, and a hard link to a file outside the
  tree; case-insensitive filesystems, where a differently-cased spelling of the
  root reads as an escape, which no client produces and which cannot be fixed by
  folding case without breaking case-sensitive volumes; Windows, which nothing
  in Ox is exercised on.

## Implementation plan

1. Add `internal/workspace/workspace.go`: the package comment stating the rule
   and why it runs through `os.Root`; `Canonical`; `Workspace`, `New`, and
   `Root`; `Resolve`; `Path` with `String`, `Absolute`, and `Open`; and the
   unexported `resolveExisting`, `inside`, `escapes`, and error mapping.
2. Add `internal/workspace/workspace_test.go` covering the resolution, refusal,
   and open behaviors in the test plan, and
   `internal/workspace/fifo_unix_test.go` for the named pipe.
3. Replace `session.cwd` with `session.workspace`, delete `canonicalDirectory`,
   and have `NewSession` call `workspace.Canonical`, then `workspace.New`, then
   `config.Resolve` with `Root()`. Keep the log key `cwd`, which is the name the
   client used.
4. Add the end-to-end test for an unlistable working directory, with the mode
   restored in `t.Cleanup` and a skip when running as root, and the test that a
   session created through a symlinked working directory reads its workspace
   configuration from the real directory.
5. Update `AGENTS.md`.

## Documentation updates

- `AGENTS.md`: check off the workspace confinement roadmap item.
- `AGENTS.md`: add a Workspace section after Configuration stating the current
  contract — a session has one canonical working directory, resolved through
  symlinks and required to be listable; every path a tool takes resolves against
  it and is refused if it lands outside, whether by an absolute path, a `..`
  climb, or a symlink leaving the tree; a path the client spells through a
  symlink still resolves; and only regular files can be opened.

## Impact assessment

- Code paths affected: one new package, and `internal/agent/session.go`'s
  working-directory handling. The prompt turn, the provider client,
  configuration resolution, and credentials are untouched.
- Data, protocol, or schema impact: none. `session/new` gains one reason to
  return `-32602`, an unlistable working directory.
- Dependency or API impact: none. `os.Root`, `filepath`, and
  `syscall.O_NONBLOCK` are all standard library.
- Deliberately deferred, and none of it half-built here: the file read, write,
  and exact edit operations, including atomic replace and read-evidence guards,
  which are the next item and belong in this package; the gitignore-aware walk
  that glob and grep need, which needs the root held open across a traversal;
  readable-only roots, which exist for shell-output spill files that Ox has no
  shell tool to produce; telling the model the workspace root and the path
  rules, which needs a system prompt; `fs/*` delegation to the client, which
  will send `Absolute()` and cannot be enforced by `os.Root` because the client
  does the reading; and process-level sandboxing of a future shell tool, which
  is a subsystem to agree on separately.

## Validation

- Tests to write and run: `go test -race -count=1 ./...`.
- Static checks: `make check`, which runs `dprint check`, `gofmt`,
  `go vet ./...`, and `staticcheck ./...`.
- Manual verification: create a directory, symlink it, and start a session
  through the symlink; confirm the log reports the real directory. Confirm
  `session/new` on a directory with its read permission removed fails `-32602`,
  and succeeds once the permission is restored.
