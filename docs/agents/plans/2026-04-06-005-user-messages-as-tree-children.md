# Render user messages as tree children with blue circles

## Goal

Change the TUI so that user messages render as circle-prefixed tree children (like assistant messages and tool calls) instead of as `❯`-prefixed roots. The entire conversation becomes a single tree hanging off the session banner, with user messages as top-level children and their responses/tools as nested children underneath. User message circles are blue.

## Desired outcome

Before:
```
Session: 20260406-185135-171  (type a message · Esc = cancel turn · Ctrl+C = exit)
❯ First user message
└─ ● Response to first
❯ Second user message
├─ ● tool_call(arg: "value")
└─ ● Response to second
```

After:
```
Session: 20260406-185135-171  (type a message · Esc = cancel turn · Ctrl+C = exit)
├─ ● First user message
│  └─ ● Response to first
└─ ● Second user message
   ├─ ● tool_call(arg: "value")
   └─ ● Response to second
```

- User message `●` is `Color.Blue`.
- All other circle colors unchanged (tool state colors, assistant white, subagent lifecycle).
- Session banner (`BubbleStyle.Plain`) unchanged.

## How we got here

The request has clear scope and concrete before/after examples. No brainstorming needed. The existing tree-drawing model in `EventList` already handles `├─`/`└─` connectors for Circle children — the change is to promote user messages from root nodes into top-level Circle nodes and nest their children one level deeper.

## Approaches considered

### Option A — Modify EventList rendering inline

- Summary: Keep `BubbleStyle.User` for grouping but change `Render()` to emit user items as Circle children instead of `❯` roots. Add a nesting prefix for their children.
- Pros: No new types, no changes to `Program.cs`, minimal API surface change.
- Cons: EventList's `Render()` gets more complex (nested chrome computation).
- Failure modes: Incorrect `isLast` computation for the top-level tree when Plain items are interspersed.

### Option B — Create UserTurnRenderable (like SubagentRenderable)

- Summary: Wrap each user message + its children into a `UserTurnRenderable` with an inner `EventList`. Add it to the outer EventList as a Circle child.
- Pros: Delegates nesting to the inner EventList — outer EventList stays simple.
- Cons: Requires a new type. Requires changes to `Program.cs` to manage the "current turn" renderable. The inner EventList uses 5-col child chrome, but the desired nesting uses 3-col prefix + 5-col child chrome = 8 cols total — not achievable with standard child continuation (which gives 5 + 5 = 10 cols).
- Failure modes: Chrome width mismatch requires either a custom EventList rendering mode or manual row construction, defeating the purpose of reuse.

## Recommended approach

**Option A** — modify EventList's rendering logic directly.

- Why: The change is contained to one file. No new types needed. The calling code in `Program.cs` doesn't change at all — `eventList.Add(userMsg)` still uses the default `BubbleStyle.User`. The rendering simply interprets that style differently.
- Key tradeoffs: EventList's `Render()` method becomes somewhat more complex, but the complexity is well-contained in clearly named helper methods.

## Related code

- `src/Ur.Tui/Rendering/EventList.cs` — All rendering changes happen here. Contains `BubbleStyle` enum, tree chrome constants, `Render()` method, and all `Make*Row` helpers.
- `src/Ur.Tui/Rendering/Color.cs` — `Color.Blue` already exists (SGR 34). No changes needed.
- `src/Ur.Tui/Program.cs:92-94` — Where user messages are added as `BubbleStyle.User`. No changes needed.
- `tests/Ur.Tests/TuiRenderingTests.cs` — `EventListTests` class. Every test that checks for `❯` needs updating; new tests needed for nesting and blue circles.

## Current state

- User messages render as tree roots with `❯` prefix (2-col chrome: `❯ `).
- Circle children render with `├─ ● ` / `└─ ● ` prefix (5-col chrome).
- Each User item starts a new independent tree group. Groups have no visual connection to each other.
- SubagentRenderable already demonstrates nested tree rendering (inner EventList produces rows, outer EventList adds continuation chrome).
- The `BubbleStyle.User` enum value controls grouping in `Render()` — each User starts a new group of subsequent Circle children.

## Structural considerations

The change fits cleanly within EventList's existing responsibility (tree rendering). No new abstractions are needed. The `BubbleStyle.User` enum variant stays — it still serves the grouping role — but its rendering changes from "root with `❯`" to "top-level Circle child with blue `●`".

**Chrome width model:**
- Level 1 (user messages): `ChildChrome` = 5 cols (`├─ ● ` or `└─ ● `)
- Level 2 (nested children): `NestChrome` + `ChildChrome` = 3 + 5 = 8 cols
- NestChrome breakdown: `│  ` (non-last parent) or `   ` (last parent) — the nested branch aligns under the parent's `●` at col 3.

**User text wrapping:** User messages wrap at `availableWidth - ChildChrome` (same width as any other Circle child). Continuation rows use standard `MakeChildContinuationRow` (5 cols: `│    ` or `     `).

**Nested child alignment:** A nested child's `├`/`└` appears at col 3, directly under the parent user's `●`. Content starts at col 8. This is visually natural — branches emerge from the circle.

**isLast computation:** The top-level tree's last item is determined by scanning all groups. The last User item (or last orphan Circle if no Users) gets `└─`; all earlier top-level items get `├─`.

## Implementation plan

### EventList.cs — constants and docs

- [x] Add `NestChrome = 3` constant with doc comment explaining the 3-col nesting prefix.
- [x] Remove `PromptChar` constant (no longer used).
- [x] Remove `RootChrome` constant (no longer used).
- [x] Update `BubbleStyle.User` doc comment: user messages now render as blue Circle children, not `❯` roots. Still starts a new tree group.
- [x] Update `EventList` class doc comment: replace the target visual with the new rendering (user messages as `├─ ●` / `└─ ●` children with nested responses).

### EventList.cs — new helper methods

- [x] Add `PrependNestPrefix(CellRow row, bool isLastParent) → CellRow` — prepends `│  ` (BrightBlack vertical + 2 spaces) or `   ` (3 spaces) to an existing row. This composes with `MakeChildRow`/`MakeChildContinuationRow` to produce nested chrome.
- [x] Add `RenderNestedChild(List<CellRow> target, int index, bool isLastNested, bool isLastParent, int availableWidth)` — renders a Circle child as a nested item. Calls `MakeChildRow`/`MakeChildContinuationRow` for the inner chrome, then `PrependNestPrefix` for the outer nesting. Content width = `availableWidth - NestChrome - ChildChrome`.

### EventList.cs — rewrite Render()

- [x] Rewrite the main rendering loop:
  1. **Partition** into groups (same logic as current: Plain items standalone, User items start groups, orphan Circles form groups).
  2. **Pre-compute last top-level index:** scan backwards to find the last User item (or last orphan Circle if no Users). This determines which top-level item gets `└─`.
  3. **Render each group:**
     - **Plain:** verbatim (unchanged).
     - **User group:** render the User item as a top-level Circle child with `Color.Blue` using `MakeChildRow`/`MakeChildContinuationRow`. Then render its Circle children as nested items via `RenderNestedChild`.
     - **Orphan group:** render orphan Circle items as top-level Circle children (same as current, but now aware of the global `isLast` context).
- [x] Remove `RenderRoot` method (replaced by rendering users as circle children).
- [x] Remove `MakeRootRow` and `MakeRootContinuationRow` methods (no longer needed).

### Tests — update existing

- [x] `Render_SingleUser_PromptGlyphOnFirstRow` → rename to `Render_SingleUser_CircleGlyphWithBlueColor`. Assert `└─ ● ` prefix (5 cols) with blue circle at col 3 (not `❯` at col 0).
- [x] `Render_UserWithOneChild_LastChildBranch` → user gets `└─ ●` (blue, last top-level), child becomes nested: `   └─ ●` (3-space nest + last child). Assert chrome positions at cols 0-4 and 3-7 respectively.
- [x] `Render_UserWithTwoChildren_BranchAndLastBranch` → user gets `└─ ●` (blue, last top-level). Children are nested: first gets `   ├─ ●` (non-last nested), second gets `   └─ ●` (last nested). Assert positions.
- [x] `Render_UserContinuationWithChildren_VerticalBar` → user text wraps with standard child continuation (`     `, 5 spaces — last top-level). Nested child gets `   └─ ●`. Assert column positions.
- [x] `Render_UserContinuationWithoutChildren_NoVerticalBar` → user text wraps with standard child continuation. Since it's the last (and only) top-level item: `     ` (5 spaces, last). No nested children. Assert.
- [x] `Render_TwoGroups_NoBlankRowBetweenThem` → two user groups in one continuous tree. First user: `├─ ●` (blue, non-last). Second user: `└─ ●` (blue, last). Each user's child is nested. Assert `├` at row 0, nested `└` at row 1 col 3, `└` at row 2 col 0, nested `└` at row 3 col 3.
- [x] `Render_OrphanGroupFollowedByUserGroup_NoBlankSeparator` → orphan is a top-level `├─ ●` (non-last since user follows). User is `└─ ●` (last). Assert positions.
- [x] `Render_PlainFollowedByUserGroup_NoSeparator` → Plain renders verbatim. User gets `└─ ●` (blue). User's child is nested: `   └─ ●`. Assert all positions.
- [x] `Render_CircleColor_PassedThroughToGlyph` → circle is now a nested child. Assert `●` at col 6 (NestChrome + 3 = col 6 in the nested row) with the specified color.
- [x] `Render_CircleColorDefault_UsesWhite` → same position adjustment.

### Tests — add new

- [x] `Render_UserCircle_HasBlueColor` — add a single User item, verify `●` at col 3 has `Color.Blue` foreground.
- [x] `Render_NestedChild_HasCorrectChrome` — user + one child. Verify nested child row has `└─ ● ` starting at col 3 (after 3-space nest prefix for last parent). Content at col 8.
- [x] `Render_NestedChildContinuation_NonLastNested` — user + two nested children, first child wraps. Verify the continuation row has `│  │    ` prefix (nest `│  ` + child continuation `│    ` = 8 cols) for a non-last nested child.
- [x] `Render_NestedChildContinuation_LastNested` — user + one nested child that wraps. Verify the continuation row has `        ` (8 spaces: 3-space nest for last parent + 5-space last-child continuation).
- [x] `Render_TwoUsers_ContinuousTree` — two users each with children. Verify the entire output forms one continuous tree: first user `├─ ●` (col 0), its children nested with `│  ` prefix (col 0 = `│`); second user `└─ ●` (col 0), its children nested with `   ` prefix (col 0 = space).

### Validation

- [x] Run `make inspect`, read `inspection-results.txt`, fix any issues.
- [x] Verify all tests pass.
- [ ] Manual verification: run the TUI, send two messages with tool calls, confirm the visual matches the desired output.

## Open questions

- Should the user message text color remain `Color.White` (as currently set in `Program.cs:92`), or should it change to `Color.Blue` to match the circle? The plan assumes text stays white and only the `●` glyph is blue, matching the stated requirement "user message circles should be blue."

White text!!!