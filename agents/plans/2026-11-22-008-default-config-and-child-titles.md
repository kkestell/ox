# Write the default config, and give child titles a convention

## Goal

Two items from `docs/todo.md`:

1. **Write the default config on first start** (Configuration). When Ox finds
   no configuration file, it writes the built-in default there, creating its
   directory, and loads it from the file, so the user can begin editing it.
2. **Consistent child titles** (Child sessions). Agents title children
   inconsistently: `Plan: …`, `Plan …`, `F8 plan: …`, `Implement …` beside
   `Work: …`, and an ID such as `(F2)` at the end, where the sidebar truncates
   it. `create_session`'s description gives one convention.

## Related code

- `internal/config/config.go` — `Load` substitutes the embedded `defaultConfig`
  bytes for a missing file and checks them like any file.
- `internal/config/default.json` — the built-in catalog, embedded.
- `cmd/oxd/main.go:43` and `cmd/ox/main.go:48` — both binaries call
  `config.Load` on the same path. `make dev` (`cmd/ox/web/dev.mjs`) starts them
  together, so on a fresh config directory both may find the file missing at
  once.
- `internal/server/mcp.go:186` — `create_session`'s description and its
  `title` property.
- `internal/server/mcp_test.go:395` — titles are capped at 48 characters.

## Decisions

- **Load writes the file.** The rule lives in `config.Load`, the one place that
  reads the file, so `oxd`, `ox`, and tests all behave alike: whichever starts
  first writes it. `Load` never writes over an existing file.
- **The written file is the embedded bytes, read back.** On a missing file,
  `Load` writes `defaultConfig` byte for byte (not a re-marshalled `Config`),
  then reads the path and proceeds as for any file. The file and the in-memory
  default are the same bytes by construction, and every first start proves the
  written file loads.
- **Atomic publication.** `Load` writes the bytes to a temporary file in the
  same directory (`os.CreateTemp(dir, ".config-*")`), closes it, then
  `os.Link`s it to the path and removes the temporary file. `Link` fails with
  `fs.ErrExist` instead of replacing, so a concurrent starter that lost the
  race, or a file the user created meanwhile, is kept; `Load` treats
  `ErrExist` as success and reads the path. No reader can see a partly written
  file. Without this, `ox` and `oxd` starting together could read each other's
  half-written file and refuse to start.
- **Permissions.** The directory is created with `os.MkdirAll(dir, 0o700)`, as
  the XDG spec asks for a directory created to hold a file, and as
  `cmd/oxd`'s `listen` does. `CreateTemp` makes the file `0o600`, which the link
  keeps. An existing directory's mode is left alone.
- **Failures.** A directory or file that cannot be created makes `Load` return
  `write default configuration to <path>: <err>`, and `oxd` refuses to start,
  saying why, as **Configuration checks** describes for other configuration
  faults. A failed write removes its temporary file. A path that exists but is
  unreadable, empty, or invalid is refused as today and left unchanged. A
  dangling symlink at the path reads as missing, its link fails with
  `ErrExist`, and the read that follows returns the not-exist error.
- **`-config`.** A path given with `-config` that does not exist is written the
  same way; the flag chooses the file, and the rule does not depend on how it
  was chosen.
- **Requirements:** **Built-in catalog** changes (below). **Configuration
  checks** holds: an invalid file still stops `oxd` with its reason. **File
  locations** holds: the file is written where it is read.
- **Construction:** one source for the default (`default.json`'s bytes), and
  one rule for a missing file (`Load`).
- **Trust boundaries:** `Load` now creates a directory and a file at the
  configuration path, which only the user's own flags and environment choose,
  with user-only permissions.
- **Title convention.** The description's sentence on `title` becomes:
  "title is its label in Ox, shown beneath yours in a narrow sidebar that cuts
  off long titles: write it as `Stage: topic`, where Stage is one capitalized
  word for the kind of work, the same word for the same kind across your
  children, such as the name of the command the child runs, and topic is the
  fewest words that tell it from its siblings, any ID first, such as
  `Review: F2 child start outside lock`." The `title` property's description
  becomes "the child's label in Ox, one line, as `Stage: topic`". The 48
  character limit stays where it is checked.

## Check your assumptions

1. **Ox writes its configuration file on first start.**
   - **Plan:** The first time Ox starts without a configuration file, it writes
     the built-in catalog to that file (`~/.config/ox/config.json` by default)
     and loads it from there, so the user edits a complete file instead of
     writing one from the documentation. If the file cannot be written, Ox
     refuses to start and says why. Proposed requirement text, replacing
     **Built-in catalog**:

     > - **Built-in catalog.** When Ox starts without a configuration file, it
     >   writes one for the user to edit, offering Claude Opus, Sonnet, and
     >   Haiku 5.5 with their effort levels and the manual, auto, acceptEdits,
     >   and plan modes, and Ur with DeepSeek V4.1 Flash and GLM 5.3 Flash at
     >   low, high, and max effort and Muse Spark 1.3 Contributor at minimal,
     >   low, medium, high, xhigh, and max effort, in manual mode.

     Old text:

     > - **Built-in catalog.** Without a configuration file, Ox offers Claude
     >   Opus, Sonnet, and Haiku 5.5 with their effort levels and the manual,
     >   auto, acceptEdits, and plan modes, and Ur with DeepSeek V4.1 Flash and
     >   GLM 5.3 Flash at low, high, and max effort and Muse Spark 1.3
     >   Contributor at minimal, low, medium, high, xhigh, and max effort, in
     >   manual mode.
   - **Alternative:** Fall back to the in-memory catalog when the file cannot
     be written. Ox would start on a read-only config directory, but the user
     would get no file and no word why.
   - **User:** Approved the plan and the requirement text as written.
2. **Child titles read `Stage: topic`.**
   - **Plan:** Agents title children like `Plan: default config`, `Work: F2
     child start`, `Review: F2 child start`, so siblings line up by stage and
     the part that tells them apart survives the sidebar's cut-off.
   - **Alternative:** Topic first, `F2 child start: Review`. Siblings working
     on different topics would sort apart more clearly, but the stage, the part
     that differs among one topic's plan, work, and review children, would be
     what the sidebar cuts off.
   - **User:** Approved the plan.

## Test plan

- New `TestLoadWritesTheDefault` in `internal/config/config_test.go`: no
  existing test checks the filesystem after `Load`. It loads a path beneath a
  directory that does not exist yet and asserts:
  - the file's bytes equal `defaultConfig`, the file is `0o600` and its new
    directory `0o700`;
  - a second `Load` of the written file returns a `Config` `reflect.DeepEqual`
    to the first;
  - eight concurrent `Load`s of another missing path all succeed with equal
    configurations, and the directory holds only `config.json` afterward (no
    temporary file left behind).
  It fails before the change, which writes no file.
- In the same test: an existing invalid file (`{`) is refused and its bytes are
  unchanged afterward; a path whose parent is a regular file returns an error
  starting with `write default configuration to`. The invalid-file case guards
  against a change that overwrites; it passes before the change, as the
  original behavior it protects.
- The title convention is guidance to the model, observable only in what a
  model chooses to write; per `AGENTS.md` ("Verification"), live tests do not
  assert model wording, so it has no automated test.

## Implementation plan

1. `internal/config/config.go`: on `fs.ErrNotExist`, call a new unexported
   `writeDefault(path)` that creates the directory, writes the temporary file,
   links it, and removes it, as in Decisions; then read the path again. Wrap a
   `writeDefault` error as `write default configuration to %s: %w`.
2. `internal/config/config_test.go`: add `TestLoadWritesTheDefault`.
3. `internal/server/mcp.go`: replace the `title` sentence of
   `create_session`'s description and the `title` property's description with
   the text in Decisions.
4. Apply the requirement change and documentation updates.

## Requirement changes

- **Changed:** **Built-in catalog**, old and new text as in Check your
  assumptions, entry 1.

## Documentation updates

- `docs/protocol.md` ("Configuration"): "A missing file selects the built-in
  catalog of Claude Code and Ur." becomes "A missing file is written with the
  built-in catalog of Claude Code and Ur, creating its directory, and then
  read."
- `docs/architecture.md`: the `config` row's "Reading the configuration file,
  or the built-in default," becomes "Reading the configuration file, writing
  the built-in default there when it is missing,".
- `docs/todo.md`: remove **Write the default config on first start** and
  **Consistent child titles**, with the Configuration and Child sessions
  headings, which are left empty.
