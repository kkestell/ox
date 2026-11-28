# Tidy TabArea, tabs.ts, and the composer box

## Goal

Remove repeated casts and helpers in `TabArea.tsx` and `tabs.ts`, and share the
composer's prompt box between `SessionView.tsx` and `NewSessionView.tsx`.
No behavior changes. Resolves [#207](https://github.com/kkestell/ox/issues/207).

## Related code

- `cmd/ox/web/src/tabs.ts` — `findGroup`, `focusedTab` (casts at `findGroup(...) as Group`), `setMode`, `reveal`, `mapGroup`, `locate`.
- `cmd/ox/web/src/TabArea.tsx` — `TabArea` (`focused`), `GroupView` (two inline IIFEs for `reveal` and `mode`, beside `shownPath`), `PhoneBar` (`focused`).
- `cmd/ox/web/src/SessionView.tsx` — `<form class="box">`, plus `sendOnEnter` and `keepPromptFocus`, which `NewSessionView.tsx` imports from it.
- `cmd/ox/web/src/NewSessionView.tsx` — the same form with a Send-only button and `autofocus`.
- `cmd/ox/web/src/Attachments.tsx` — `Thumbnails`, `AttachButton`, `pasteImages`, `createImageDrop`.
- `cmd/ox/web/src/tabs.test.mjs` — existing layout tests.

## Decisions

- **Construction:** `focusedGroup(layout): Group` is the one place that asserts the focused id names a group (the layout keeps `focused` valid). `focusedTab` uses it; `TabArea` and `PhoneBar` call `focusedGroup` / `focusedTab` and carry no cast.
- **GroupView accessors:** `reveal` and `mode` become derived accessors next to `shownPath`, reading `active()` once each.
- **mapTab:** `mapTab(layout, key, change: (tab: Tab) => Tab): Layout` does `locate` + `mapGroup` + `tabs.map` for the tab with `tabKey` equal to `key`, returning the layout unchanged when none exists. `setMode` calls it with `file:${path}` and `reveal` with `"changes"`. `reveal` keeps its `openTab` step first.
- **Optional composer item: included.** The two forms are identical except for the action button (Send/Stop vs Send only) and `autofocus`, so sharing makes both simpler rather than adding a flag-laden abstraction. New `Composer.tsx` exports `PromptBox(props: { control: SessionControl; onSubmit; dragging: boolean; autofocus?: boolean; children })`, rendering `<form class="box">` with `Thumbnails`, the `field` div, `AttachButton`, the `<textarea>`, and `props.children` as the action button. `sendOnEnter` and `keepPromptFocus` move there (module-private), so `NewSessionView` no longer imports from `SessionView`. The outer `composer` div stays in each view, since its contents differ (approvals, settings). `autofocus` is passed as a plain boolean; reads stay in JSX.
- **Requirements:** none touched; Send, Stop, attach, drop, and tab behavior are unchanged.

## Check your assumptions

None.

## Test plan

- `tabs.test.mjs` already covers `setMode`, `reveal`, and `focusedTab`; add a `focusedGroup` case only if none covers it. No new e2e: the existing specs for prompt entry, attach, drop, Stop, new session, tabs, and the phone bar observe the refactor.
- Checks: `make test` (Biome, Go, Node suites) and `make e2e`.

## Implementation plan

1. `tabs.ts`: add and export `focusedGroup`, use it in `focusedTab`; add `mapTab`, use it in `setMode` and `reveal`.
2. `TabArea.tsx`: use `focusedGroup` in `TabArea`, `focusedTab` in `PhoneBar`; replace the two IIFEs with accessors; drop the unused `findGroup`/`Group` imports.
3. Add `Composer.tsx` with `PromptBox`, `sendOnEnter`, `keepPromptFocus`; use it in `SessionView.tsx` and `NewSessionView.tsx`; remove the moved functions and now-unused imports.
4. Run `npm run fix`, `make test`, `make e2e`.

## Requirement changes

None.
