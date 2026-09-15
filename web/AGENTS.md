# AGENTS.md

THIS FILE MUST BE KEPT UP TO DATE AT ALL TIMES

This directory is the Hugo site for Ox. `ox.dev` is its placeholder public URL until the site is deployed. The Hugo binary is **built from source on demand** via `go install` into `./bin/` (gitignored), pinned to a single version, so the site builds with no host-level toolchain other than `make` and a working Go install. Ox is written in Go, so the same toolchain builds both the site and the agent.

## Tech Stack

- **Static site generator:** Hugo (pinned version, built from source via `go install`)
- **Build orchestration:** `make`
- **Templates:** Hugo's Go templates under `layouts/`
- **Styles:** plain CSS under `static/css/`

## Repository Layout

- `bin/` — **Generated, gitignored.** Hugo binary built by `go install` on first build.
- `hugo.toml` — Site config (title, baseURL, locale, disabled kinds).
- `layouts/` — Templates. `baseof.html` is the shell; `home.html` renders the homepage; `_default/single.html` and `_default/list.html` render content pages.
- `static/` — Files copied verbatim into the site root (e.g. `static/css/main.css` → `/css/main.css`).
- `content/` — Markdown content. The `/docs/` landing page lives under `content/docs/`.
- `archetypes/` — Unused Hugo archetype directory. The site has no `assets/`, `data/`, `i18n/`, or `themes/`.
- `public/` — Build output. **Generated, gitignored.**
- `resources/`, `.hugo_build.lock` — Hugo's build cache. **Generated, gitignored.**

## Commands

All commands run from this directory.

- `make build` — Build the site to `./public/` (minified). Installs Hugo on first run.
- `make serve` — Run Hugo's dev server with drafts at <http://localhost:1313>. Installs Hugo on first run.
- `make clean` — Remove `public/`, `resources/`, and the build lock. Leaves `bin/` alone so the next build doesn't re-install.

## Conventions

- Don't depend on a system-installed `hugo`. Always go through `make` (or call `./bin/hugo` directly) so we stay on the pinned version.
- Bumping Hugo means changing `HUGO_VERSION` in the `Makefile`, deleting `bin/hugo` so it re-installs, and noting the new version here.
- The site uses plain CSS only — no SCSS/SASS — so the non-extended Hugo build that `go install` produces is sufficient. If we ever add SCSS, switch to a build path that produces extended Hugo (cgo + libsass), or precompile styles before invoking Hugo.
- Site config lives in `hugo.toml` only — don't introduce `config.toml` or per-environment config files until there's a real need.
- Taxonomies, RSS, and sitemap output are disabled in `hugo.toml`. Re-enable explicitly if/when needed instead of relying on Hugo's defaults.
- Doc page frontmatter is TOML (`+++ … +++`) with `title` only — add `weight` when a page needs explicit ordering.
- Links between docs pages use trailing-slash style rather than `.md` filenames so they resolve on the rendered site.
