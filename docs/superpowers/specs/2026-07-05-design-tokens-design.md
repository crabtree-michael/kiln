# Kiln design-system tokenization — design

**Date:** 2026-07-05
**Source of truth:** `docs/ui/Kiln Colors.html` (the "Kiln colors" design-system page; copied into the repo alongside the older Voice Screen reference it supersedes for color/type/spacing decisions).

## Goal

Replace the frontend's two divergent ad-hoc palettes — the `/debug` shell's old dark
`--kiln-*` set (App.css) and the primary screen's local oklch vars
(PrimaryScreen.css / TicketDetail.css) — with the single Kiln design system:
paper/ink/fire/glaze/ember ramps, semantic aliases, one type scale, spacing,
radii, shadows, and motion tokens, in both light and dark themes. After this
change, no component stylesheet contains a raw color literal; everything reads
semantic tokens.

## Decisions (agreed with user)

1. `/debug` shell is restyled as the **dark theme** of the new system ("Kiln at
   night"); the `--kiln-*` palette is deleted.
2. The primary screen **follows the system preference** (`prefers-color-scheme`)
   between light paper and dark, including system chrome (`theme-color`,
   `color-scheme`, safe-area strips).
3. Fonts are **self-hosted via npm**: `@fontsource-variable/hanken-grotesk`,
   `@fontsource-variable/newsreader`, `@fontsource-variable/spline-sans-mono`.
   The Google Fonts `<link>`/preconnects leave index.html.
4. **Strict flat fidelity**: flat `--surface-page` page background (no radial
   gradient), flat `--accent` fills for the kiln glyph and Accept buttons.
   Token shadows only on floating surfaces. The design file wins wherever it
   and the current look disagree.

## Architecture

### 1. Token layer — `frontend/src/styles/tokens.css` (new)

Lifted from the design file, verbatim where possible:

- Primitive ramps on `:root`: `--paper-0/50/100/200/300`, `--ink-900/700/500/400`,
  `--fire-50/100/300/500/600/700`, `--glaze-100/500/700`, `--ember-100/500/700`.
- Semantic aliases (what components consume): `--surface-page/card/raised/inset/subtle`,
  `--text-body/secondary/muted/faint/on-accent`, `--accent/-hover/-press/-soft/-faint`,
  `--calm/-strong/-soft`, `--warn/-strong/-soft`, `--danger/-soft`,
  `--border-default/strong/hairline`, `--focus-ring`, `--scrim`.
- Dark overrides under `[data-theme='dark']` exactly as in the design file
  (including dark shadow set).
- Typography: `--font-sans/editorial/mono` (pointing at the fontsource families),
  the `--text-display/title/heading/body/body-strong/small/caption/editorial/mono`
  shorthand scale, `--tracking-caps/display`.
- Spacing `--space-1..16`, layout (`--page-gutter`, `--content-max`,
  `--marketing-max`, `--touch-target`), radii `--radius-sm/md/lg/xl/pill`,
  shadows `--shadow-card/raised/overlay`, motion (`--ease-settle`,
  `--ease-in-out`, `--duration-fast/base/slow`, `--pulse-duration`).
- Base element styles: `* { box-sizing }`, body font/color/background,
  `::selection`, `:focus-visible`, link styling.

Imported first in `main.tsx`, before any component CSS.

Naming collision note: the design file defines `--text-body` twice — once as a
color alias and once as a font shorthand (the later block silently clobbers
the color). We do not replicate that bug. Resolution: the color keeps
`--text-body`; the font-shorthand scale is renamed with a `--type-*` prefix
(`--type-display`, `--type-body`, `--type-caption`, …). This is the only
deliberate deviation from the design file.

### 2. Fonts

`main.tsx` imports the three fontsource variable packages. `index.html` drops
the Google Fonts preconnect/link. Font stacks in `--font-*` keep the design
file's fallbacks (Seravek/Segoe UI/system-ui; Iowan Old Style/Georgia; SF
Mono/Cascadia Mono).

### 3. Theme mechanism

One mechanism: `data-theme="light" | "dark"` on `<html>`.

- New tiny module `frontend/src/theme.ts`: reads
  `matchMedia('(prefers-color-scheme: dark)')`, sets `data-theme`, subscribes
  for live changes. Route-aware: `/debug` forces `dark` regardless of system
  preference (per decision 1); `/` follows the system.
- `ThemeColorSync` keeps the `theme-color` meta in step with the active theme's
  `--surface-page` (`#FAF6EF` light / `#16110D` dark) so PWA safe-area strips
  and browser chrome match in both themes. `color-scheme` follows `data-theme`
  (drives UA form controls/scrollbars/overscroll).
- All `:has(...)` scoping hacks in App.css/PrimaryScreen.css for
  color-scheme/background are replaced by the `data-theme` attribute +
  `--surface-page` on `html`/`body`.

### 4. Primary screen restyle (PrimaryScreen.css, TicketDetail.css primary skin)

- Local token block on `[data-role='primary-screen']` (`--ember`, `--ink`,
  `--muted`, `--faint`, `--hairline`, `--surface`, `--paper`, `--paper-flat`)
  is deleted; every rule remaps to semantic tokens:
  - `--ember*` → `--accent` / `--accent-hover` / `--accent-press` / `--accent-soft`
  - `--ink`/`--ink-soft` → `--text-body` / `--text-secondary`
  - `--muted`/`--faint` → `--text-muted` / `--text-faint`
  - `--hairline`/`--hairline-faint` → `--border-default` / `--border-hairline`
  - `--surface` → `--surface-card`; `--paper`/`--paper-flat` → `--surface-page`
    (flat — the radial gradient and the gradient/flat split disappear;
    `--paper-flat` consumers just use `--surface-page`)
  - one-off oklch/rgba literals (card body inks, toast text, blocked-reason
    red, backdrop scrim, shadows) → nearest semantic token
    (`--text-secondary`, `--danger`, `--scrim`, `--shadow-*`).
- Gradients: kiln glyph, empty-state mark, Accept buttons become flat
  `--accent` (text `--text-on-accent`), with `--shadow-card`/`--shadow-raised`
  as appropriate.
- Type: `--font-sans` (Hanken Grotesk) replaces Space Grotesk;
  `--font-mono` (Spline Sans Mono) replaces IBM Plex Mono for labels/meta;
  `--font-editorial` (Newsreader) takes the calm register — the all-clear
  empty-state title. Font sizes snap to the `--type-*` scale where a scale
  step matches (±1px); intentionally-tuned in-between sizes (e.g. 10.5px mono
  caps) keep their px values but take tokenized family/tracking.
- Motion: existing transitions/animations adopt `--duration-*`/`--ease-*`
  where a token matches. The breathing loops (status dot, empty-state pulse,
  mic glow) adopt `--pulse-duration` (2400ms) — the design file calls this
  "the one deliberate loop in the system", so its timing wins over the
  current 1.6s.
- **Sanctioned computed color:** the volume-reactive mic glow
  (`--mic-level`-driven `oklch(calc(...))`) cannot be a flat token. It is
  rebuilt around the fire hue (fire-500/600 region) with its calc structure
  intact, documented inline as the one computed color in the codebase.
- Radii snap to `--radius-*` where within 2px of a step; the mic/dock circles
  stay 50%.

### 5. Debug shell restyle (App.css, TicketDetail.css base rules)

- `--kiln-*` block deleted. `/debug` renders under `data-theme='dark'`, so the
  same semantic tokens paint it "Kiln at night".
- Status mapping (semantic, per the design file's meaning):
  - blocked → fire: `--danger` border/text, `--danger-soft` fills
  - working → ember: `--warn`
  - done → glaze: `--calm`
  - ready → neutral: `--text-muted` / `--border-strong`
  - old blue accent (chips, notification tags, chat send, user bubble,
    focus/hover) → `--accent` (+ `--text-on-accent` on fills)
- Structural layout rules unchanged; only colors/fonts/radii/shadows tokenized.
  The debug shell keeps its utilitarian density (no redesign, just re-skin).

### 6. index.html / PWA chrome

- `theme-color` meta default → `#FAF6EF` (ThemeColorSync overrides at runtime
  per theme/route). If a manifest declares colors, update to match
  (`background_color` `#FAF6EF`).

## Error handling

Pure styling change — no new runtime failure modes. `theme.ts` guards
`matchMedia` absence (jsdom) by defaulting to light. `/debug` forcing dark must
not leak to `/` on client-side route changes (theme re-evaluated on route
change, same place ThemeColorSync already hooks).

## Testing

- Existing unit/DOM tests must pass unchanged — styling keys off `data-*`
  attributes, not classNames; DOM snapshots (Dock) should not change. Any test
  asserting theme-color/color-scheme behavior (ThemeColorSync tests, if any)
  updated deliberately.
- New small unit test for `theme.ts` (system preference → data-theme; /debug
  forces dark).
- Gate (end-to-end-development skill): `npm run lint`, type-check, full test
  suite in `frontend/`.
- Visual verification (verify/run skills): drive the real app; screenshot `/`
  and `/debug` in light and dark; check safe-area/theme-color chrome, fonts
  actually rendering (Hanken Grotesk, not fallback), mic glow still
  volume-reactive.

## Out of scope

- No component markup/behavior changes (except the minimal theme.ts wiring).
- No redesign of debug-shell layout.
- No marketing pages (`--marketing-max` ships as a token, unused).
- Backend untouched.
