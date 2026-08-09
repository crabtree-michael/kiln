// What a captured snapshot is CALLED — the name the settings capture form sends
// with `POST /api/projects/{id}/snapshots`.
//
// The user is not asked for one. A snapshot's name is an opaque provider-side
// identifier whose only job is to be recognisable in the picker later, and the
// two facts worth recognising are which project it came from and when it was
// taken. Asking for a name got a date typed by hand or a word that meant
// something for a week; deriving it means every snapshot in a project's catalog
// sorts by age under one stem.
//
// This is deliberately the same shape the SERVER derives for the capture a
// finished ticket triggers (`snapshotName` in backend/cmd/kiln/adapters.go), so
// the two ways a snapshot can be created leave one kind of name behind rather
// than two — `pacman-20260809141304` however it was taken. The two
// implementations are separate on purpose: the server's name is the capture's
// idempotency key on an at-least-once outbox and must be derived from an instant
// the board stamped, which is not a thing a browser can be handed. If you change
// the shape here, change it there.

/** Reduces free text to lowercase alphanumerics joined by single dashes, with no
 * leading or trailing dash — the conservative shape an opaque provider-side name
 * is safe in. Mirrors the backend's `slugify`. */
function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

/** The stem a snapshot falls back to when the project's name slugs to nothing
 * (punctuation only, or no name at all), so the timestamp always has a word in
 * front of it. */
const FALLBACK_STEM = 'kiln';

/** Pads a number to two digits — the timestamp's every field but the year. */
function pad(value: number): string {
  return String(value).padStart(2, '0');
}

/** `<project>-YYYYMMDDHHMMSS` in UTC: `Pac-Man` at 14:13:04 on 2026-08-09 →
 * `pac-man-20260809141304`. UTC because the name is compared against snapshots
 * taken by the server, which has no idea what the user's clock reads; the
 * timestamp carries no separators of its own so the one dash in the name is the
 * one between the stem and the time.
 *
 * `at` is passed in rather than read here, so the caller stamps the name at the
 * moment the capture is actually requested. */
export function snapshotNameFor(projectName: string, at: Date): string {
  const stem = slugify(projectName);
  const timestamp = [
    String(at.getUTCFullYear()),
    pad(at.getUTCMonth() + 1),
    pad(at.getUTCDate()),
    pad(at.getUTCHours()),
    pad(at.getUTCMinutes()),
    pad(at.getUTCSeconds()),
  ].join('');
  return `${stem === '' ? FALLBACK_STEM : stem}-${timestamp}`;
}
