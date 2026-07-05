# Kiln Design-System Tokenization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the frontend's two ad-hoc palettes with the single Kiln design system (`docs/ui/Kiln Colors.html`): tokens in `tokens.css`, self-hosted fonts, a `data-theme` mechanism (light/dark), and full restyles of the primary screen and `/debug` shell.

**Architecture:** One global `tokens.css` (primitive ramps + semantic aliases, light on `:root`, dark on `[data-theme='dark']`) imported first in `main.tsx`. A pure `theme.ts` module resolves route + system preference to a theme; `ThemeColorSync` applies it to `<html data-theme>` and the `theme-color` meta. Component CSS consumes only semantic tokens.

**Tech Stack:** React 18, Vite 5, vitest, plain CSS (no framework — 07 §5 D4), @fontsource-variable packages.

**Spec:** `docs/superpowers/specs/2026-07-05-design-tokens-design.md`

## Global Constraints

- Package manager is **pnpm** (`packageManager: pnpm@11.7.0`); run everything from `frontend/`. Both `pnpm-lock.yaml` AND `package-lock.json` are tracked — after any dependency change run `npm install --package-lock-only` to keep the npm lockfile in sync.
- CSS keys off `data-*` attributes, never classNames. No component markup changes except the theme wiring in Task 2. Existing DOM snapshots must not change.
- After Task 5, `grep -rnE '#[0-9a-fA-F]{3,8}|oklch|rgba\(' src --include='*.css' | grep -v tokens.css` must return ONLY the sanctioned mic-glow block in PrimaryScreen.css (marked `/* sanctioned computed color */`).
- The only deliberate deviation from the design file: the font-shorthand scale is prefixed `--type-*` (the file's second `--text-body` definition is a self-clobbering bug).
- Gate for every task: `pnpm run check` (lint + typecheck + 204-test suite) green before commit.
- Node >= 22.

---

### Task 1: Token layer + self-hosted fonts

**Files:**
- Create: `frontend/src/styles/tokens.css`
- Modify: `frontend/src/main.tsx` (imports), `frontend/index.html` (drop Google Fonts, theme-color), `frontend/vite.config.ts:74-75` (manifest colors), `frontend/package.json` (deps)

**Interfaces:**
- Produces: every `--paper-*/--ink-*/--fire-*/--glaze-*/--ember-*` primitive; semantic tokens `--surface-page/card/raised/inset/subtle`, `--text-body/secondary/muted/faint/on-accent`, `--accent[-hover/-press/-soft/-faint]`, `--calm[-strong/-soft]`, `--warn[-strong/-soft]`, `--danger[-soft]`, `--border-default/strong/hairline`, `--focus-ring`, `--scrim`; fonts `--font-sans/editorial/mono`; type scale `--type-display/title/heading/body/body-strong/small/caption/editorial/mono`, `--tracking-caps/display`; `--space-1/2/3/4/5/6/8/10/12/16`; `--page-gutter/content-max/marketing-max/touch-target`; `--radius-sm/md/lg/xl/pill`; `--shadow-card/raised/overlay`; `--ease-settle/in-out`, `--duration-fast/base/slow`, `--pulse-duration`. Tasks 3–4 consume these names exactly.

- [ ] **Step 1: Add font packages**

```bash
cd frontend
pnpm add @fontsource-variable/hanken-grotesk @fontsource-variable/newsreader @fontsource-variable/spline-sans-mono
npm install --package-lock-only
```

- [ ] **Step 2: Create `frontend/src/styles/tokens.css`**

```css
/* ─────────────────────────────────────────────
   Kiln design tokens — the single source of styling truth.
   Lifted from docs/ui/Kiln Colors.html (the Kiln color-system page).
   Paper is the resting state. Fire means "you're needed."
   Glaze (sage) is the all-clear. Ember (amber) is caution.

   One deliberate deviation from that file: it defines --text-body twice
   (color alias, then font shorthand — the second silently clobbers the
   first). Here the color keeps --text-body and the font-shorthand scale
   is prefixed --type-*.
   ───────────────────────────────────────────── */

:root {
  color-scheme: light;

  /* Base ramps — paper (warm neutrals) */
  --paper-0: #faf6ef; /* page */
  --paper-50: #fffcf5; /* card */
  --paper-100: #f4ede0; /* subtle fill */
  --paper-200: #efe6d4; /* inset */
  --paper-300: #e4d8c1; /* deep inset / track */

  /* Ink (warm near-black) */
  --ink-900: #221c15;
  --ink-700: #4f463a;
  --ink-500: #7a6f5e;
  --ink-400: #a2977f;

  /* Fire (attention) */
  --fire-50: #fbefe9;
  --fire-100: #f8ddd1;
  --fire-300: #f08a66;
  --fire-500: #e4442e;
  --fire-600: #c93318;
  --fire-700: #a32a12;

  /* Glaze (calm / all clear) */
  --glaze-100: #e7ead8;
  --glaze-500: #6e7d57;
  --glaze-700: #55613f;

  /* Ember (caution) */
  --ember-100: #f6e7c8;
  --ember-500: #b97f24;
  --ember-700: #8e5f14;

  /* ── Semantic aliases ─────────────────────── */
  --surface-page: var(--paper-0);
  --surface-card: var(--paper-50);
  --surface-raised: #fffdf8;
  --surface-inset: var(--paper-200);
  --surface-subtle: var(--paper-100);

  --text-body: var(--ink-900);
  --text-secondary: var(--ink-700);
  --text-muted: var(--ink-500);
  --text-faint: var(--ink-400);
  --text-on-accent: #fff9f0;

  --accent: var(--fire-500);
  --accent-hover: var(--fire-600);
  --accent-press: var(--fire-700);
  --accent-soft: var(--fire-100);
  --accent-faint: var(--fire-50);

  --calm: var(--glaze-500);
  --calm-strong: var(--glaze-700);
  --calm-soft: var(--glaze-100);

  --warn: var(--ember-500);
  --warn-strong: var(--ember-700);
  --warn-soft: var(--ember-100);

  --danger: var(--fire-600);
  --danger-soft: var(--fire-100);

  --border-default: #e6ddca;
  --border-strong: #d3c7ae;
  --border-hairline: rgba(34, 28, 21, 0.1);

  --focus-ring: rgba(228, 68, 46, 0.45);
  --scrim: rgba(34, 28, 21, 0.42);

  /* ── Typography ───────────────────────────────
     Hanken Grotesk = voice of the product (UI, display)
     Newsreader     = the calm register (quiet copy, editorial)
     Spline Sans Mono = the agents (output, diffs, repo names) */
  --font-sans: 'Hanken Grotesk Variable', Seravek, 'Segoe UI', system-ui, sans-serif;
  --font-editorial: 'Newsreader Variable', 'Iowan Old Style', Georgia, serif;
  --font-mono: 'Spline Sans Mono Variable', 'SF Mono', 'Cascadia Mono', monospace;

  /* Scale — mobile-first */
  --type-display: 600 2.125rem/1.15 var(--font-sans); /* 34px — screen titles */
  --type-title: 600 1.5rem/1.25 var(--font-sans); /* 24px — section titles */
  --type-heading: 600 1.1875rem/1.3 var(--font-sans); /* 19px — card headings */
  --type-body: 400 1rem/1.5 var(--font-sans); /* 16px — default */
  --type-body-strong: 600 1rem/1.5 var(--font-sans);
  --type-small: 400 0.875rem/1.45 var(--font-sans); /* 14px — secondary */
  --type-caption: 500 0.78125rem/1.4 var(--font-sans); /* 12.5px — labels, meta */
  --type-editorial: 400 1.125rem/1.55 var(--font-editorial); /* 18px — calm register */
  --type-mono: 400 0.8125rem/1.55 var(--font-mono); /* 13px — agent output */

  --tracking-caps: 0.08em; /* uppercase captions only */
  --tracking-display: -0.015em;

  /* ── Spacing — 4px base. Calm needs air. ── */
  --space-1: 4px;
  --space-2: 8px;
  --space-3: 12px;
  --space-4: 16px;
  --space-5: 20px;
  --space-6: 24px;
  --space-8: 32px;
  --space-10: 40px;
  --space-12: 48px;
  --space-16: 64px;

  /* Layout */
  --page-gutter: 20px;
  --content-max: 640px;
  --marketing-max: 1120px;
  --touch-target: 44px;

  /* ── Radii — controls are pills; surfaces are soft ── */
  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 16px;
  --radius-xl: 24px;
  --radius-pill: 999px;

  /* ── Shadows — warm-tinted, soft, low. Paper sits flat; only floating
     things (toasts, dialogs, menus) cast real shadows. ── */
  --shadow-card: 0 1px 2px rgba(46, 32, 18, 0.05);
  --shadow-raised: 0 2px 6px rgba(46, 32, 18, 0.07), 0 10px 28px rgba(46, 32, 18, 0.09);
  --shadow-overlay: 0 6px 16px rgba(46, 32, 18, 0.12), 0 24px 56px rgba(46, 32, 18, 0.18);

  /* ── Motion — settle, don't bounce ── */
  --ease-settle: cubic-bezier(0.22, 1, 0.36, 1);
  --ease-in-out: cubic-bezier(0.65, 0, 0.35, 1);
  --duration-fast: 140ms;
  --duration-base: 240ms;
  --duration-slow: 420ms;

  /* The orb pulse — the one deliberate loop in the system */
  --pulse-duration: 2400ms;
}

/* ── Kiln at night — warm charcoal, never blue-black. Fire glows brighter. ── */
[data-theme='dark'] {
  color-scheme: dark;

  --surface-page: #16110d;
  --surface-card: #211a13;
  --surface-raised: #2a211a;
  --surface-inset: #100c08;
  --surface-subtle: #1c1610;

  --text-body: #f3ecdf;
  --text-secondary: #cdc2b0;
  --text-muted: #998c79;
  --text-faint: #6f6355;
  --text-on-accent: #fff9f0;

  --accent: #f0563c;
  --accent-hover: #f4694f;
  --accent-press: #d9401f;
  --accent-soft: rgba(240, 86, 60, 0.16);
  --accent-faint: rgba(240, 86, 60, 0.09);

  --calm: #97a87c;
  --calm-strong: #b4c49a;
  --calm-soft: rgba(151, 168, 124, 0.16);

  --warn: #d9a247;
  --warn-strong: #e9bc6e;
  --warn-soft: rgba(217, 162, 71, 0.16);

  --danger: #f0563c;
  --danger-soft: rgba(240, 86, 60, 0.16);

  --border-default: #372c21;
  --border-strong: #4b3d2e;
  --border-hairline: rgba(243, 236, 223, 0.09);

  --focus-ring: rgba(240, 86, 60, 0.5);
  --scrim: rgba(10, 7, 5, 0.6);

  --shadow-card: 0 1px 2px rgba(0, 0, 0, 0.3);
  --shadow-raised: 0 2px 6px rgba(0, 0, 0, 0.35), 0 10px 28px rgba(0, 0, 0, 0.4);
  --shadow-overlay: 0 6px 16px rgba(0, 0, 0, 0.45), 0 24px 56px rgba(0, 0, 0, 0.55);
}

/* ── Base element styles ── */
* {
  box-sizing: border-box;
}

/* The root paints --surface-page so safe-area strips (viewport-fit=cover)
   continue the app's own background in BOTH themes — replaces the old
   `html:has([data-role='primary-screen'])` gradient hack. */
html {
  background: var(--surface-page);
}

body {
  margin: 0;
  font: var(--type-body);
  color: var(--text-body);
  background: var(--surface-page);
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}

::selection {
  background: var(--accent-soft);
  color: var(--text-body);
}

:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 2px;
}

a {
  color: var(--text-body);
  text-decoration: underline;
  text-decoration-color: var(--accent);
  text-underline-offset: 3px;
}
```

- [ ] **Step 3: Wire imports in `frontend/src/main.tsx`**

Add at the top of the import block (fonts first, then tokens, before any component import so component CSS wins the cascade where it overrides base styles):

```ts
import '@fontsource-variable/hanken-grotesk';
import '@fontsource-variable/newsreader';
import '@fontsource-variable/spline-sans-mono';
import '@/styles/tokens.css';
```

- [ ] **Step 4: Update `frontend/index.html`**

Remove the two `<link rel="preconnect">` lines and the Google Fonts stylesheet `<link>` (and the "Primary-screen (08) type" comment above them). Change the theme-color meta to the paper token value and update its comment to say ThemeColorSync overrides it per theme/route:

```html
<meta name="theme-color" content="#faf6ef" />
```

- [ ] **Step 5: Update PWA manifest colors in `frontend/vite.config.ts`**

Lines ~74-75: `theme_color: '#f3efee'` → `'#faf6ef'`, `background_color: '#f3efee'` → `'#faf6ef'` (manifest is static; light paper is the install-time default).

- [ ] **Step 6: Verify**

```bash
pnpm run check && pnpm run build
```
Expected: lint, typecheck, 204 tests pass; build succeeds and emits woff2 assets.

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "feat(web): add Kiln design tokens + self-hosted variable fonts"
```

---

### Task 2: Theme mechanism (`theme.ts` + `ThemeColorSync`)

**Files:**
- Create: `frontend/src/theme.ts`
- Test: `frontend/src/theme.test.ts`
- Modify: `frontend/src/components/ThemeColorSync.tsx` (full rewrite)

**Interfaces:**
- Consumes: `[data-theme='dark']` overrides from tokens.css (Task 1).
- Produces: `type Theme = 'light' | 'dark'`; `resolveTheme(pathname: string, prefersDark: boolean): Theme`; `applyTheme(theme: Theme): void`; `THEME_COLORS: Record<Theme, string>` (`light: '#faf6ef'`, `dark: '#16110d'` — must equal the two `--surface-page` values in tokens.css). Task 4 relies on `/debug` forcing dark.

- [ ] **Step 1: Write the failing test `frontend/src/theme.test.ts`**

```ts
// Route + system preference → data-theme (design spec 2026-07-05 §3):
// /debug always renders "Kiln at night"; / follows the OS.
import { afterEach, describe, expect, it } from 'vitest';
import { applyTheme, resolveTheme, THEME_COLORS } from '@/theme';

describe('resolveTheme', () => {
  it('follows the system preference on /', () => {
    expect(resolveTheme('/', false)).toBe('light');
    expect(resolveTheme('/', true)).toBe('dark');
  });

  it('forces dark on /debug regardless of preference', () => {
    expect(resolveTheme('/debug', false)).toBe('dark');
    expect(resolveTheme('/debug', true)).toBe('dark');
  });
});

describe('applyTheme', () => {
  afterEach(() => {
    delete document.documentElement.dataset.theme;
    document.querySelector('meta[name="theme-color"]')?.remove();
  });

  it('stamps data-theme on <html> and syncs the theme-color meta', () => {
    const meta = document.createElement('meta');
    meta.setAttribute('name', 'theme-color');
    document.head.appendChild(meta);

    applyTheme('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(meta.getAttribute('content')).toBe(THEME_COLORS.dark);

    applyTheme('light');
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(meta.getAttribute('content')).toBe(THEME_COLORS.light);
  });

  it('tolerates a missing theme-color meta', () => {
    expect(() => applyTheme('light')).not.toThrow();
    expect(document.documentElement.dataset.theme).toBe('light');
  });
});
```

- [ ] **Step 2: Run it — expect failure**

```bash
pnpm vitest run src/theme.test.ts
```
Expected: FAIL — cannot resolve `@/theme`.

- [ ] **Step 3: Create `frontend/src/theme.ts`**

```ts
// One theme mechanism: `data-theme` on <html>, consumed by tokens.css.
// `/debug` is always "Kiln at night" (07's developer shell); `/` follows the
// system preference. ThemeColorSync owns the matchMedia subscription and
// calls these on route/preference changes.
export type Theme = 'light' | 'dark';

// Must match the two `--surface-page` values in styles/tokens.css — this is
// what the OS chrome (status bar / address bar / safe-area strips) paints.
export const THEME_COLORS: Record<Theme, string> = {
  light: '#faf6ef',
  dark: '#16110d',
};

export function resolveTheme(pathname: string, prefersDark: boolean): Theme {
  if (pathname.startsWith('/debug')) {
    return 'dark';
  }
  return prefersDark ? 'dark' : 'light';
}

export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  document
    .querySelector('meta[name="theme-color"]')
    ?.setAttribute('content', THEME_COLORS[theme]);
}
```

- [ ] **Step 4: Run the test — expect pass**

```bash
pnpm vitest run src/theme.test.ts
```
Expected: 4 tests PASS.

- [ ] **Step 5: Rewrite `frontend/src/components/ThemeColorSync.tsx`**

Replace the whole file:

```tsx
// Applies the resolved theme (route + system preference → theme.ts) to the
// document: `data-theme` on <html> for tokens.css, and the `theme-color`
// meta for OS chrome / safe-area strips. Subscribes to the OS
// prefers-color-scheme so the app follows live theme flips.
import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import { applyTheme, resolveTheme } from '@/theme';

export function ThemeColorSync(): null {
  const location = useLocation();
  useEffect(() => {
    // jsdom (vitest) has no matchMedia — default to light there.
    if (typeof window.matchMedia !== 'function') {
      applyTheme(resolveTheme(location.pathname, false));
      return;
    }
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const sync = () => applyTheme(resolveTheme(location.pathname, query.matches));
    sync();
    query.addEventListener('change', sync);
    return () => query.removeEventListener('change', sync);
  }, [location.pathname]);
  return null;
}
```

- [ ] **Step 6: Full gate**

```bash
pnpm run check
```
Expected: all tests pass (204 + 4 new).

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "feat(web): data-theme mechanism — /debug forces dark, / follows system"
```

---

### Task 3: Primary screen restyle (PrimaryScreen.css + TicketDetail.css primary skin)

**Files:**
- Modify: `frontend/src/components/PrimaryScreen.css` (full-file token remap), `frontend/src/components/TicketDetail.css:140-228` (primary skin block)

**Interfaces:**
- Consumes: all semantic tokens (Task 1). No new names produced.

This is a mechanical remap plus five deliberate redesign points. Do NOT change selectors, layout properties (flex/grid/position/size), or the `data-*` contract — colors, fonts, radii, shadows, and animation timings only.

- [ ] **Step 1: Delete the local token blocks**

Remove the `html:has([data-role='primary-screen'])` and `body:has([data-role='primary-screen'])` rules entirely (tokens.css now paints `--surface-page` on html/body in both themes; the gradient is gone per strict-flat decision). Remove the local var definitions on `[data-role='primary-screen']` (`--ember`, `--ember-strong`, `--ember-soft`, `--ink`, `--ink-soft`, `--muted`, `--faint`, `--hairline`, `--hairline-faint`, `--surface`, `--paper-flat`) and their explanatory comments. Update the file-top comment to name the new system (`docs/ui/Kiln Colors.html`, tokens in `src/styles/tokens.css`, Hanken Grotesk / Spline Sans Mono / Newsreader).

- [ ] **Step 2: Apply the variable remap across the whole file (and TicketDetail.css lines 140-228)**

| Old | New |
|---|---|
| `var(--ember)` | `var(--accent)` |
| `var(--ember-strong)` | `var(--accent-hover)` |
| `var(--ember-soft)` | `var(--accent-soft)` |
| `var(--ink)` | `var(--text-body)` |
| `var(--ink-soft)` | `var(--text-secondary)` |
| `var(--muted)` | `var(--text-muted)` |
| `var(--faint)` | `var(--text-faint)` |
| `var(--hairline)` | `var(--border-default)` |
| `var(--hairline-faint)` | `var(--border-hairline)` |
| `var(--surface)` | `var(--surface-card)` |
| `var(--paper)`, `var(--paper-flat)` | `var(--surface-page)` |

Then the color literals:

| Old literal | New token |
|---|---|
| `oklch(0.6 0.01 40 / 0.09)` (feed-status hover bg) | `var(--surface-inset)` |
| `box-shadow: 0 14px 34px oklch(0.4 0.02 40 / 0.16)` (dropdown) | `var(--shadow-overlay)` |
| `oklch(0.4 0.012 40)` (feed-card-tag) | `var(--text-secondary)` |
| `oklch(0.26 0.012 40)`, `oklch(0.24 0.012 40)` (card bodies) | `var(--text-body)` |
| `oklch(0.42 0.14 27)` (blocker body, blocked-reason text) | `var(--danger)` |
| `oklch(0.985 0.004 40)` (accept text) | `var(--text-on-accent)` |
| `oklch(0.3 0.012 40)` (toast-text) | `var(--text-secondary)` |
| `oklch(0.52 0.19 26)` (toast-title) | `var(--accent)` |
| `oklch(0.5 0.01 40)` (mic capsule/arc/stem) | `var(--text-muted)` |
| `oklch(0.86 0.006 40)` (mic border) | `var(--border-strong)` |
| `oklch(0.9 0.006 40)` (image border) | `var(--border-default)` |
| `oklch(0.55 0.01 40)` (inactive pulse dot) | `var(--text-muted)` |
| `oklch(0.58 0.2 27 / 0.22)` (spinner track) | `var(--accent-soft)` |
| `oklch(0.58 0.2 27 / 0.08)` (blocked-reason bg, TicketDetail) | `var(--accent-faint)` |
| `oklch(0.28 0.02 40 / 0.4)` (detail backdrop) | `var(--scrim)` |
| pulse rings `0 0 0 3px oklch(0.58 0.2 27 / 0.16)` / `0 0 0 4px … / 0.04` | `0 0 0 3px var(--accent-soft)` / `0 0 0 4px var(--accent-faint)` |
| glow/shadow tints `oklch(0.58 0.2 27 / …)` on dots, toasts, accept buttons | drop the glow; use `var(--shadow-card)` (dots/pills) or `var(--shadow-raised)` (buttons, image) |
| `rgba(70, 28, 20, …)` shadows | `var(--shadow-card)` (1-3px blur) / `var(--shadow-raised)` (larger) |

- [ ] **Step 3: Five deliberate redesign points**

(a) **Flat accent fills** — kiln glyph, empty-state mark, proposal Accept (PrimaryScreen.css) and detail Accept (TicketDetail.css): replace each `background: linear-gradient(160deg, oklch(...), var(--ember-strong))` with `background: var(--accent)`. Accept buttons additionally get `border-radius: var(--radius-pill)` (design: "controls are pills") and hover/active states:

```css
[data-role='proposal-accept']:hover {
  background: var(--accent-hover);
}

[data-role='proposal-accept']:active {
  background: var(--accent-press);
  transform: translateY(1px);
}
```

(same pair for `[data-role='primary-screen'] [data-role='detail-accept']`).

(b) **Fonts** — `font-family: 'Space Grotesk', system-ui, sans-serif` → `var(--font-sans)`; every `font-family: 'IBM Plex Mono', monospace` → `var(--font-mono)`. The all-clear title takes the calm register:

```css
[data-role='feed-empty-title'] {
  font: 500 1.5rem/1.25 var(--font-editorial);
  letter-spacing: var(--tracking-display);
  color: var(--text-body);
}
```

(c) **Pulse timing** — every `1.6s` breathing loop (`kiln-status-pulse` usages, `kiln-mic-glow`) → `var(--pulse-duration)`.

(d) **Motion tokens** — `0.12s`/`0.15s` transitions → `var(--duration-fast)`; `0.2s`/`0.22s`/`0.24s` → `var(--duration-base)`; `cubic-bezier(0.22, 1, 0.36, 1)` → `var(--ease-settle)`. Keep the keyword `ease`/`ease-out`/`linear`/`step-end` where a rule uses one.

(e) **Sanctioned computed color** — the `--mic-level`-driven glow keeps its `oklch(calc(…))` structure (a volume-reactive color cannot be a flat token). Re-anchor its hue to the fire ramp (hue 25 → 29, matching fire-500 ≈ oklch(0.60 0.21 29)) and mark it:

```css
  /* sanctioned computed color: the one non-token color in the codebase —
     a volume-reactive fire glow (fire-500 hue), driven by --mic-level. */
```

- [ ] **Step 4: Radii snap (within 2px of a token step only)**

`7px` (feed-status hover) → `var(--radius-sm)`; `10px` (accept buttons — now pill per Step 3a); `12px` (dropdown, toast-pill) → `var(--radius-md)`; `14px` (say-pill) → `var(--radius-md)`; `16px` (detail sheet, card image) → `var(--radius-lg)`; `8px` focus-ring radius → `var(--radius-sm)`. Leave the glyph's asymmetric radii and all `50%` circles as-is.

- [ ] **Step 5: Verify no stray colors, tests, visual**

```bash
grep -nE '#[0-9a-fA-F]{3,8}|oklch|rgba\(' src/components/PrimaryScreen.css | grep -v 'sanctioned' # only the marked glow block lines may remain
pnpm run check
```
Expected: grep shows only the mic-glow calc lines; all tests pass (DOM snapshots untouched).

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(web): restyle primary screen on Kiln design tokens (flat paper, fire accent, new type)"
```

---

### Task 4: Debug shell restyle (App.css + TicketDetail.css base rules)

**Files:**
- Modify: `frontend/src/App.css` (full-file token remap), `frontend/src/components/TicketDetail.css:1-125` (base rules)

**Interfaces:**
- Consumes: semantic tokens (Task 1); `/debug` renders under `data-theme='dark'` (Task 2), so the same tokens paint it "Kiln at night".

- [ ] **Step 1: Delete the old palette and scheme hacks**

In App.css remove: the whole `--kiln-*` var block from `:root` (keep the file-top comment, updated), the `color-scheme: light;` line and its rationale comment (tokens.css + data-theme own color-scheme now), the `:root:has(.app-shell)` block entirely, and the `body { … }` rule (tokens.css owns body font/color/background).

- [ ] **Step 2: Apply the remap in both files**

| Old | New |
|---|---|
| `var(--kiln-bg)` | `var(--surface-page)` |
| `var(--kiln-surface)` | `var(--surface-card)` |
| `var(--kiln-surface-raised)` | `var(--surface-raised)` |
| `var(--kiln-border)` | `var(--border-default)` |
| `var(--kiln-text)` | `var(--text-body)` |
| `var(--kiln-text-dim)` | `var(--text-muted)` |
| `var(--kiln-accent)` | `var(--accent)` |
| `var(--kiln-ready)` | `var(--border-strong)` on the ticket border-left; `var(--calm)` on the connection chip `connected` state; `var(--calm)` on the notification `preview` kind |
| `var(--kiln-working)` | `var(--warn)` |
| `var(--kiln-done)` | `var(--calm)` |
| `var(--kiln-blocked)` | `var(--danger)` |
| `var(--kiln-blocked-bg)` | `var(--danger-soft)` |
| `var(--kiln-danger)` | `var(--danger)` |
| `color: #0b0b0f` (user bubble + send button text on accent) | `var(--text-on-accent)` |
| `rgba(0, 0, 0, 0.6)` (detail backdrop) | `var(--scrim)` |
| `box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.5)` (detail sheet) | `var(--shadow-overlay)` |
| `color: var(--kiln-surface)` on `[data-role='detail-accept']` | `var(--text-on-accent)` |

Status-mapping rationale (design-file semantics): fire = "you're needed" (blocked), ember = caution (working), glaze = all-clear (done), neutral ink = ready.

- [ ] **Step 3: Debug-shell polish to tokens**

- Uppercase labels (`board-column > h2`, `notification-tag`, `chat-message-role`, `ticket-detail-meta dt`): `letter-spacing` → `var(--tracking-caps)`.
- Radii: `999px` → `var(--radius-pill)`; `0.3rem`/`0.35rem`/`0.4rem`/`4px` → `var(--radius-sm)`; `0.5rem`/`0.6rem` → `var(--radius-sm)` on small controls, `var(--radius-md)` on cards/columns/detail sheet, `var(--radius-lg)` nowhere (debug stays dense).
- Buttons with accent fill (`chat form button`, `detail-accept`): `border-radius: var(--radius-pill)`.
- `[data-role='ticket-card'][data-state='blocked']` keeps its `box-shadow: 0 0 0 1px var(--danger)`.

- [ ] **Step 4: Verify**

```bash
grep -nE '#[0-9a-fA-F]{3,8}|oklch|rgba\(|--kiln' src/App.css src/components/TicketDetail.css
pnpm run check
```
Expected: grep returns nothing; all tests pass.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(web): restyle /debug shell as Kiln-at-night on design tokens, drop --kiln-* palette"
```

---

### Task 5: Full verification + visual pass

**Files:** none new (fixes only if verification finds issues).

- [ ] **Step 1: Whole-repo stray-color audit**

```bash
cd frontend
grep -rnE '#[0-9a-fA-F]{3,8}|oklch|rgba\(' src --include='*.css' | grep -v styles/tokens.css | grep -v sanctioned
grep -rn "fonts.googleapis\|Space Grotesk\|IBM Plex Mono\|Instrument Serif\|f3efee" src index.html vite.config.ts
```
Expected: both return nothing (first grep: the sanctioned mic-glow lines are excluded by marker; if any line of that block still matches, extend the inline marker comment so each matching line's rule block is clearly the sanctioned one, or adjust the grep to `-A6` context review).

- [ ] **Step 2: Gate + build**

```bash
pnpm run check && pnpm run format:write && pnpm run build
```
Expected: lint, typecheck, tests, prettier, production build all green. If `format:write` changed files, re-run `pnpm run check`, amend or commit the formatting.

- [ ] **Step 3: Visual verification (verify/run skills)**

Bring the app up (per local-environment skill / `pnpm dev`), then screenshot and inspect:
- `/` light (OS light): flat paper page, fire accents, Hanken Grotesk rendering (compare a glyph against system-ui to confirm the webfont loaded), mic glow still volume-reactive.
- `/` dark (emulate `prefers-color-scheme: dark`): Kiln-at-night surfaces, `theme-color` meta `#16110d`, `<html data-theme="dark">`.
- `/debug`: dark tokens, blocked=fire / working=ember / done=glaze mapping visible, accent chat send button.
- Ticket-detail overlay on both routes.

- [ ] **Step 4: Commit any fixes; final commit**

```bash
git add -A && git commit -m "chore(web): verification fixes for design-token rollout" # only if needed
```

Then use superpowers:finishing-a-development-branch to decide merge/PR.
