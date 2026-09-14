# Tailwind and shadcn base theme

## Goal

The browser client's appearance is one hand-written layout stylesheet. Put
Tailwind and shadcn into the Bun build behind a single base theme and move the
existing controls onto shadcn's primitives. The information architecture does
not change: the same regions, the same controls, and the same accessible names.
Appearance beyond the theme itself belongs to the shell and conversation styling
work that follows.

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
file by hand and use `add` from then on. Pin `"style": "new-york-v4"`, the Radix
style whose generated components import `cn` from the `cn` package and `Slot`
from `radix-ui` rather than a local `@/lib/utils`, so no utils module is
vendored even though the strict config schema still requires its alias.

`shadcn add` installs `cn` and `radix-ui` and nothing else, but the generated
components import `class-variance-authority`, and `select.tsx` imports
`lucide-react`. Add both explicitly. The icons shadcn ships inside `Select` are
decorative chevrons and checks within a control whose accessible name comes from
its trigger, so they do not displace a name.

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

Adopt shadcn's components wherever the client already has a control for them:
`Button`, `Input`, `Textarea`, `Label`, `Select`, and `Collapsible`. Do not
vendor presentational components such as `Card`, because deciding what earns a
surface is a shell-styling decision. Two of them change the accessible role, and
that is accepted rather than avoided. Radix `Select` replaces the three native
`<select>` controls for model, mode, and reasoning with a trigger and a
portalled listbox, so the role becomes `combobox` plus `option`. Radix
`Collapsible` replaces the seven `<details>`/`<summary>` disclosures, so each
summary becomes a `button`. Both are accessible implementations, and a native
`<select>` is barely styleable and would read as unfinished beside the rest of
the theme.

Accessible names still do not change. Every `aria-label`, heading, and label
text stays as it is, which for `Select` means carrying the existing `aria-label`
onto `SelectTrigger`.

The file inputs stay native, because shadcn has no file-input component and
`<input type="file">` is what native file selection requires.

This task is the toolchain, the theme, and the component swap. It is not a
visual pass. Past the classes that keep the sidebar beside the primary column,
it chooses no spacing, type scale, density, or arrangement for any region: the
shell and conversation styling tasks own those, and anything decided here would
be replaced by them. The expected intermediate state is stock shadcn primitives
at default density, and it should look unfinished.

## Test plan

- `client/e2e/smoke.spec.ts` keeps every assertion whose control did not change
  role, including the sidebar-beside-main geometry, the file inputs, and every
  `getByRole("button")` and `getByLabel` that still resolves.
- The mode control moves from `getByLabel("Mode").selectOption("plan")` and
  `toHaveValue` to opening the combobox by its unchanged name and choosing the
  option by its text. The `"Add context"` and `"Conversation actions"`
  disclosures move from a text click on a `<summary>` to a click on a button of
  the same name. These are the only spec edits this change should need; another
  failure means a name moved, which is a defect rather than a test to update.
- Radix renders the select listbox in a portal outside `main`, so assertions
  scoped to `main` or to the conversation region must not expect option text
  inside them.
- `tsc --noEmit` covers the vendored components under the client's existing
  `strict` and `noUncheckedIndexedAccess` settings.
- `bun run build` fails the check when the theme CSS is invalid, which is what
  putting the Tailwind CLI in front of `check` buys.

## Implementation plan

- Add `tailwindcss` and `@tailwindcss/cli` as dependencies and `shadcn` plus
  `tw-animate-css` as dev dependencies. Add `class-variance-authority` and
  `lucide-react`; let `shadcn add` bring in `cn` and `radix-ui`.
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
- Vendor `button`, `input`, `textarea`, `label`, `select`, and `collapsible`
  with `shadcn add`.
- Swap the native controls in `client/src/browser.tsx` for their shadcn
  equivalents, carrying each existing `aria-label` onto the element that now
  owns the accessible name. Replace the `layout` class with the utility classes
  that keep the sidebar beside the primary column, and change nothing else about
  arrangement.
- Update the model, mode, and reasoning interactions and the two disclosure
  interactions in `client/e2e/smoke.spec.ts` to the roles Radix renders.

## Documentation updates

- `eng/client-architecture.md` — Rewrite the browser-surface invariant. Neither
  "semantic HTML with native controls" nor "a stylesheet carries layout and
  appearance only" survives a vendored component library. What survives is the
  part worth keeping durable: navigating is a link, every state change is a
  button, and accessible names come from content and labels rather than from
  presentation. Name `src/components/ui` as vendored third-party presentation
  owned by the React application.
- `eng/todo.md` — Check this task off.
