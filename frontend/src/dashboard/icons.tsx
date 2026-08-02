// The dashboard's icon set (settings redesign): a handful of hand-drawn inline
// SVGs, because dependencies are gated on explicit approval (07 D4) and an icon
// font/library is exactly the kind of thing that never earns its weight for a
// dozen glyphs.
//
// Conventions, so the settings CSS can size and color every icon with one rule:
//   * a 24×24 viewBox, stroked (never filled) in `currentColor`, so an icon
//     inherits the color of whatever row/button/chip it sits in;
//   * NO width/height attributes — `[data-role='settings'] svg[data-icon]` sizes
//     them in `em`, so an icon scales with its neighbouring text;
//   * `aria-hidden` on every one: these decorate a visible text label, they never
//     replace it (the accessible name always comes from the label or an
//     `aria-label` on the control), so a screen reader must skip them;
//   * `data-icon="<name>"` both drives the sizing rule and makes the DOM
//     snapshots readable.
//
// The GitHub mark is the one exception to the stroke rule — it is a brand mark
// with an official filled path, and redrawing it as strokes would misrepresent
// it.
import type { JSX } from 'react';

/** The shared stroke geometry. Kept in one object so no icon drifts to a
 * different weight or cap style than the rest of the set. */
const STROKE = {
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.75,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
  'aria-hidden': true,
} as const;

/** Back to the app. */
export function ArrowLeftIcon(): JSX.Element {
  return (
    <svg data-icon="arrow-left" {...STROKE}>
      <path d="M15 19 8 12l7-7" />
    </svg>
  );
}

/** The Account section: a person. */
export function UserIcon(): JSX.Element {
  return (
    <svg data-icon="user" {...STROKE}>
      <circle cx="12" cy="8" r="3.75" />
      <path d="M4.75 20a7.25 7.25 0 0 1 14.5 0" />
    </svg>
  );
}

/** The Integrations section: a plug, i.e. the things Kiln connects out to. */
export function PlugIcon(): JSX.Element {
  return (
    <svg data-icon="plug" {...STROKE}>
      <path d="M9 3v5M15 3v5M6.5 8h11v3.5a5.5 5.5 0 0 1-11 0V8ZM12 17v4" />
    </svg>
  );
}

/** The Notifications section. */
export function BellIcon(): JSX.Element {
  return (
    <svg data-icon="bell" {...STROKE}>
      <path d="M18 8.5a6 6 0 1 0-12 0c0 6.5-2.5 8.5-2.5 8.5h17S18 15 18 8.5Z" />
      <path d="M13.7 20.5a2 2 0 0 1-3.4 0" />
    </svg>
  );
}

/** The Projects section: a box, the same "unit of work" shape the board uses. */
export function BoxIcon(): JSX.Element {
  return (
    <svg data-icon="box" {...STROKE}>
      <path d="M20.5 8.2v7.6a1.8 1.8 0 0 1-.9 1.6l-6.7 3.8a1.8 1.8 0 0 1-1.8 0l-6.7-3.8a1.8 1.8 0 0 1-.9-1.6V8.2a1.8 1.8 0 0 1 .9-1.6l6.7-3.8a1.8 1.8 0 0 1 1.8 0l6.7 3.8a1.8 1.8 0 0 1 .9 1.6Z" />
      <path d="m3.8 7.3 8.2 4.7 8.2-4.7M12 21v-9" />
    </svg>
  );
}

/** One project, in a project card's header. */
export function FolderIcon(): JSX.Element {
  return (
    <svg data-icon="folder" {...STROKE}>
      <path d="M3.5 7a2 2 0 0 1 2-2H9l2.2 2.6h7.3a2 2 0 0 1 2 2V18a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7Z" />
    </svg>
  );
}

/** A secret / API key. */
export function KeyIcon(): JSX.Element {
  return (
    <svg data-icon="key" {...STROKE}>
      <circle cx="15.5" cy="8.5" r="4.5" />
      <path d="M12.3 11.7 3.5 20.5M6 18l2 2M8.7 15.3l2 2" />
    </svg>
  );
}

/** The Amika sandbox credential — the spark that lights a worker. */
export function SparkIcon(): JSX.Element {
  return (
    <svg data-icon="spark" {...STROKE}>
      <path d="m12 3 2.1 5.6 5.6 2.1-5.6 2.1L12 18.4l-2.1-5.6L4.3 10.7l5.6-2.1L12 3Z" />
    </svg>
  );
}

/** A coding agent (the Devin credential). */
export function BotIcon(): JSX.Element {
  return (
    <svg data-icon="bot" {...STROKE}>
      <rect x="4" y="8" width="16" height="12" rx="3.5" />
      <path d="M12 4v4M9.5 14h.01M14.5 14h.01M10 17.2h4M2.5 12.5v3M21.5 12.5v3" />
    </svg>
  );
}

/** A non-secret identifier (the Amika Claude credential ID). */
export function IdIcon(): JSX.Element {
  return (
    <svg data-icon="id" {...STROKE}>
      <rect x="3" y="5.5" width="18" height="13" rx="2.5" />
      <circle cx="9" cy="11" r="1.9" />
      <path d="M6 16a3.4 3.4 0 0 1 6 0M15 10h3.5M15 13.5h2.5" />
    </svg>
  );
}

/** Leave the account. */
export function SignOutIcon(): JSX.Element {
  return (
    <svg data-icon="sign-out" {...STROKE}>
      <path d="M15 16.5 19.5 12 15 7.5M19.5 12H9M12 3.5H6.5a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2H12" />
    </svg>
  );
}

/** Add (a project, a secret row). */
export function PlusIcon(): JSX.Element {
  return (
    <svg data-icon="plus" {...STROKE}>
      <path d="M12 5v14M5 12h14" />
    </svg>
  );
}

/** Destructive: delete a project. */
export function TrashIcon(): JSX.Element {
  return (
    <svg data-icon="trash" {...STROKE}>
      <path d="M4 6.5h16M9.5 6.5V5a1.8 1.8 0 0 1 1.8-1.8h1.4A1.8 1.8 0 0 1 14.5 5v1.5M10 11v6M14 11v6" />
      <path d="m6.5 6.5 .9 12.1a2 2 0 0 0 2 1.9h5.2a2 2 0 0 0 2-1.9l.9-12.1" />
    </svg>
  );
}

/** Dismiss the project modal. */
export function CloseIcon(): JSX.Element {
  return (
    <svg data-icon="close" {...STROKE}>
      <path d="M6 6l12 12M18 6 6 18" />
    </svg>
  );
}

/** "This row opens something" — the affordance on a project panel. */
export function ChevronRightIcon(): JSX.Element {
  return (
    <svg data-icon="chevron-right" {...STROKE}>
      <path d="m9.5 5 7 7-7 7" />
    </svg>
  );
}

/** A worker slot / the project's agent pool, on a project panel's meta chip. */
export function UsersIcon(): JSX.Element {
  return (
    <svg data-icon="users" {...STROKE}>
      <circle cx="9" cy="8" r="3.25" />
      <path d="M3.5 19a5.5 5.5 0 0 1 11 0M16 5.2a3.25 3.25 0 0 1 0 5.6M17.5 14.2a5.5 5.5 0 0 1 3 4.8" />
    </svg>
  );
}

/** The GitHub mark — the official filled path, not a stroked redraw. */
export function GitHubIcon(): JSX.Element {
  return (
    <svg data-icon="github" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.6 7.6 0 0 1 2-.27c.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  );
}
