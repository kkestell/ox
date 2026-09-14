# Tailwind and shadcn base theme

## Goal

The browser client's appearance is one hand-written layout stylesheet. Adopt
Tailwind and shadcn in the Bun build behind a single base theme and restyle the
existing markup with it. The DOM structure, accessible roles, and accessible
names stay exactly as they are, so the whole Playwright suite passes unchanged.

## Related code

- `client/package.json` — `build` bundles `src/browser.tsx` into `public/`;
  `check` is only `tsc --noEmit`, so no current check compiles CSS.
- `client/public/styles.css` — The checked-in layout stylesheet this replaces.
  `client/public/index.html` links it and must keep doing so.
- `client/src/host.ts:35` — The static mime map already serves `.css`, and
  `client/.gitignore` already ignores the generated `public/browser.js`.
- `client/src/browser.tsx` — 1049 lines of semantic markup carrying one
  `className`. Every accessible name comes from content, `aria-label`, or
  `aria-labelledby`.
- `client/e2e/smoke.spec.ts` — The accessibility contract. It drives a native
  `<select>` (`getByLabel("Mode").selectOption`), a native file input
  (`getByLabel("Attachments").setInputFiles`), `<summary>` text clicks
  (`"Add context"`, `"Conversation actions"`), and asserts the sidebar's right
  edge is at or left of `main`'s left edge.
- `client/tsconfig.json` — Has no `paths`, which shadcn's `@/` imports need.

## Decisions

Tailwind compiles through `@tailwindcss/cli`, not through the Bun bundler.
`bun build` does not understand Tailwind v4 at-rules: importing the source CSS
from `browser.tsx` makes it emit `invalid @ rule encountered: '@theme'`,
`'@utility'`, and `'@tailwind'`. Keeping CSS out of the JS graph avoids that
entirely, and `index.html` keeps its existing `<link>`. `build` becomes two
commands: the Tailwind CLI, then the existing `bun build`.

The source CSS moves to `client/src/styles.css` and `client/public/styles.css`
becomes generated output, added to `client/.gitignore`. Tailwind v4 detects
sources automatically from the input file's directory, so putting it in `src/`
scans exactly the client sources with no `@source` directive.

`shadcn init` refuses this project — it reports that it cannot detect a
supported framework, because there is no Next, Vite, or React Router config.
`shadcn add` works fine against a hand-written `components.json`, so write that
file by hand and use `add` from then on. Pin `"style": "new-york"` and the Radix
base; the generated components import `cn` from the `cn` package and `Slot` from
`radix-ui`, not from a local `@/lib/utils`, so no utils module is vendored.

`shadcn add` installs `cn` and `radix-ui` but not `class-variance-authority`,
which every generated component imports. Add it explicitly. Add nothing else
shadcn pulls in opportunistically; in particular do not add `lucide-react`,
because no control in this task gains an icon and an icon is how an accessible
name gets lost.

`@import "shadcn/tailwind.css"` from the `shadcn` dev dependency supplies
keyframes and the `data-state` custom variants only. It carries no color tokens,
so `src/styles.css` also holds the base theme: the `:root` and dark token blocks
from shadcn's `neutral` palette (`https://ui.shadcn.com/r/colors/neutral.json`,
`cssVars.light` and `cssVars.dark`, already in oklch for v4), the
`@theme inline` mapping from those variables to `--color-*` and `--radius-*`,
and a `@layer base` rule applying `border-border`, `bg-background`, and
`text-foreground`.

The dark tokens activate from `prefers-color-scheme: dark`. There is no theme
toggle and no persisted preference: the client has one base theme that follows
the operating system.

Adopt only the shadcn components whose rendered element and accessible name are
identical to what is there now: `Button` (`<button>`), `Input` (`<input>`),
`Textarea` (`<textarea>`), `Label` (`<label>`), and `Card`. Do not adopt
`Select` or `Collapsible`. shadcn's `Select` is a Radix combobox and would break
`selectOption` and `toHaveValue` against the model, mode, and reasoning
controls; `Collapsible` would break the `<summary>` text clicks. Those controls
keep their native elements and are styled with utility classes.

Everything else is restyling in place: utility classes on the markup that
already exists. No element is added, removed, reparented, or renamed, and no
`aria-label` or heading text changes.

## Test plan

- `client/e2e/smoke.spec.ts` passes unmodified. It is the proof that roles,
  names, and the sidebar-beside-main geometry survived, and it needs no new
  assertions for a change that is only appearance.
- `tsc --noEmit` covers the vendored components under the client's existing
  `strict` and `noUncheckedIndexedAccess` settings.
- `bun run build` fails the check when the theme CSS is invalid, which is what
  putting the Tailwind CLI in front of `check` buys.

## Implementation plan

- Add `tailwindcss` and `@tailwindcss/cli` as dependencies and `shadcn` plus
  `tw-animate-css` as dev dependencies. Add `class-variance-authority`; let
  `shadcn add` bring in `cn` and `radix-ui`.
- Add `baseUrl` and a `@/*` → `./src/*` path mapping to `client/tsconfig.json`.
- Write `client/components.json` by hand with the Radix base, `"tsx": true`,
  `src/styles.css` as the theme file, and the `@/components`, `@/components/ui`,
  and `@/lib` aliases.
- Create `client/src/styles.css`: the `tailwindcss`, `tw-animate-css`, and
  `shadcn/tailwind.css` imports, the neutral light and dark token blocks, the
  dark custom variant driven by `prefers-color-scheme`, the `@theme inline`
  mapping, and the `@layer base` defaults. Delete `client/public/styles.css` and
  ignore it in `client/.gitignore`.
- Change `build` to run `@tailwindcss/cli` from `src/styles.css` to
  `public/styles.css` before `bun build`, and change `check` to run `build`
  before `tsc --noEmit` so a broken theme fails `make check`.
- Vendor `button`, `input`, `textarea`, `label`, and `card` with `shadcn add`.
- Restyle `client/src/browser.tsx`: replace the `layout` class with the sidebar
  and main utility classes that preserve the side-by-side geometry, swap the
  native elements listed above for their shadcn equivalents where the rendered
  element is unchanged, and apply utility classes to the remaining markup
  including the `<select>`, `<details>`, and `<summary>` elements that keep
  their native implementations.

## Documentation updates

- `eng/client-architecture.md` — The invariant that "a stylesheet carries layout
  and appearance only" no longer holds once appearance lives in utility classes
  and vendored components. Restate what still holds: behavior and accessible
  names never depend on presentation. Name `src/components/ui` as vendored
  third-party presentation under the React application's ownership.
- `eng/todo.md` — Check this task off.
