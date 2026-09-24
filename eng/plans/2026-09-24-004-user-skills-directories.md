# Load skills from user skills directories

## Goal

Ox loads skills only from the workspace's `.agents/skills/`. Users also want
skills that apply in every workspace. When a session becomes active, Ox loads
the skill catalog from three skills directories, highest priority first:

1. `~/.config/ox/skills/`
2. `~/.agents/skills/`
3. The workspace's `.agents/skills/`

When two directories hold a skill with the same name, the catalog keeps the one
from the higher-priority directory. A workspace therefore cannot replace a skill
the user installed. An invalid skill definition no longer fails activation. Ox
writes its path and error to stderr and leaves it out of the skill catalog.

Use [architecture](../architecture.md), [code style](../code-style.md),
[glossary](../glossary.md), [testing guidance](../testing.md), and
[`AGENTS.md`](../../AGENTS.md) as the implementation constraints.

## Related code

- `src/skills.rs` — `load` reads one `.agents/skills` directory under the
  workspace, parses each `SKILL.md`, returns the first error prefixed with the
  relative path, and sorts the catalog by name.
- `src/settings.rs` — `load` reads `$HOME` to find
  `~/.config/ox/settings.json` and fails when `HOME` is not set.
- `src/acp/prompt.rs` — `after_run` hook errors are written to stderr with
  `eprintln!`, the pattern skipped skill definitions follow.
- `src/acp.rs` — `ServerState::new`, `serve_stdio`, and the two
  `skills::load` calls in `new_session` and `load_session`. The test helpers
  `state`, `state_over`, and `write_skill`, and the tests
  `activation_captures_the_system_prompt_and_skill_catalog_once_per_process`
  and `an_invalid_skill_definition_fails_activation_with_its_path`.

## Decisions

- A higher-priority skill replaces a lower-priority skill with the same name
  without an error. This matches Claude Code, where a personal skill wins over a
  project skill with the same name.
- An invalid skill definition is skipped. This covers a `SKILL.md` that is
  missing, unreadable, not UTF-8, too large, or fails validation, including a
  skill named `compact`. A skills directory that exists but cannot be read is
  skipped the same way. Activation continues with the other skills.
- A skipped definition still takes its directory name at its priority. A
  lower-priority skill with that name stays out of the catalog, so a broken
  user skill cannot let a workspace skill run under its name. The directory
  name is the name because a valid skill's name must match its directory.
- Every definition is read and validated, including one replaced by a
  higher-priority skill, so each broken file is reported.
- `skills::load` returns the catalog and one message per skipped definition
  or skills directory. `src/acp.rs` writes each message to stderr with
  `eprintln!`, so tests can assert the messages without capturing stderr.
- A missing skills directory means no skills from that directory and no
  message.
- Messages name the skills directory as the user sees it:
  `~/.config/ox/skills/<name>/SKILL.md`, `~/.agents/skills/<name>/SKILL.md`, and
  `.agents/skills/<name>/SKILL.md`, followed by the same error text as today.
- `Skill::directory` stays the directory the skill was loaded from, so hook
  commands of a user skill run in its directory under the home directory.
- The home directory is read once at process startup and kept in
  `ServerState`, beside the global hooks. Tests give `ServerState` a home
  directory with no skills directories, so skills on the developer's machine
  never enter a test catalog.
- Headless prompts still load no skill catalog.

## Naming

- **Skill** — Change the glossary definition from the workspace's
  `.agents/skills/` to a skills directory.
- **Skill catalog** — Unchanged: the skills available to an active session,
  loaded when it becomes active.
- **Skills directory** — One of the three directories Ox loads skill
  definitions from, each holding `<name>/SKILL.md` entries. It is used in the
  glossary, the architecture document, and doc comments in `src/skills.rs`.
- **Home directory** — The directory in `$HOME`. It is `home` in code.

## Test plan

- Add `skills_directories_load_in_priority_order` in `src/acp.rs`. Its home
  directory and workspace hold:
  - `shared` in all three skills directories.
  - `personal` in `~/.agents/skills/` and the workspace.
  - One skill found in only one directory, in each of the three.

  The catalog of a new session holds each name once, sorted by name. `shared`
  has the directory under `~/.config/ox/skills/`, `personal` has the directory
  under `~/.agents/skills/`, and each other skill has its own directory.
- Rewrite `an_invalid_skill_definition_fails_activation_with_its_path` as
  `invalid_skill_definitions_are_skipped_with_their_path` in `src/skills.rs`,
  calling `skills::load` directly. It keeps the current invalid cases. For each,
  the catalog holds a valid neighbor skill, leaves the invalid one out, and
  returns a message starting `.agents/skills/<name>/SKILL.md: ` that contains
  the current error text. It also checks that:
  - An invalid definition in `~/.agents/skills/` is reported with a message
    starting `~/.agents/skills/<name>/SKILL.md: `.
  - An invalid definition in `~/.config/ox/skills/` keeps a valid workspace
    skill with the same name out of the catalog.
  - A valid definition in `~/.config/ox/skills/` does not stop an invalid
    workspace definition with the same name from being reported.
- A session with an invalid skill definition activates, and its advertised
  slash commands leave that skill out. This is one assertion in the new
  `src/acp.rs` test.
- The existing activation, dispatch, and hook tests keep their assertions and
  get a home directory with no skills directories.

## Implementation plan

1. `src/settings.rs`: move the `HOME` lookup into a public `home_dir` and use
   it in `load`.
2. `src/skills.rs`: list the three skills directories in priority order as
   pairs of a path and the name shown in messages. Load each directory with the
   current loop, turning each error into a message instead of returning it.
   Keep the first skill or skipped definition for each directory name, then
   sort the catalog by name. `load` takes the home directory and the workspace
   path and returns the catalog with the messages. Update the module and `load`
   doc comments.
3. `src/acp.rs`: add `home` to `ServerState`, passed to `ServerState::new`.
   `serve_stdio` gets it from `settings::home_dir`. Pass it to both
   `skills::load` calls and write each returned message to stderr. Give the
   test helpers a home directory with no skills directories, let `write_skill`
   take a skills directory, and add the new test. Move the invalid definition
   test to `src/skills.rs` as described above.

## Documentation updates

- `eng/glossary.md`: the Skill definition, and a new Skills directory term.
- `eng/architecture.md`: the external boundary list names skill definitions
  rather than workspace skill definitions. The "Slash commands and skills"
  section lists the three skills directories, their priority, and that invalid
  definitions are skipped. The "Capability and trust boundaries" paragraph on
  `AGENTS.md` and `SKILL.md` no longer says an invalid `SKILL.md` fails
  activation.
- `README.md`: the Skills section lists the three skills directories, their
  priority, and that an invalid skill is reported on stderr and skipped.
- `examples/skills/goal/README.md` and `examples/skills/careful/README.md`:
  mention that the skill can also be copied into `~/.config/ox/skills/` to use
  it in every workspace.
- `AGENTS.md`: the `src/skills.rs` entry describes the three skills
  directories, name priority, and skipped definitions. The `src/settings.rs`
  entry mentions the home directory.
