// The projects rail's state vocabulary (13 §5, §8, §10) — the pure half, kept
// out of the polling hook so it can be reasoned about (and tested) without any
// I/O.
//
// The rail is read peripherally, so the vocabulary is deliberately tiny and the
// contrast budget is spent once: **only `needs-you` gets the accent** (13 §4).
// If two states both draw the eye, neither does.
import type { Board } from '@/transport/transport';

/**
 * One project's glanceable state in the rail (13 §5).
 *
 * - `needs-you` — a decision is waiting: a Blocked ticket (a blocker card) or a
 *   Shaping one (a pending proposal). The one state that gets the accent.
 * - `working` — agents are mid-turn and nothing is wanted from the user. Carries
 *   the breathing indication (13 §8), never the accent.
 * - `quiet` — the resting state, and the state Kiln is trying to keep you in
 *   (13 §1). Expressed as absence: no mark at all.
 * - `unknown` — we have not heard from this project yet (first paint, or its
 *   poll failed). Renders exactly like `quiet` — absence — because inventing a
 *   mark for "we don't know" is the kind of manufactured activity 13 §1 rules
 *   out. It is a distinct value only so the rail never *claims* quiet it hasn't
 *   verified, and so a failed poll can be told apart from a genuine all-clear.
 */
export type ProjectState = 'needs-you' | 'working' | 'quiet' | 'unknown';

/**
 * Derives a project's rail state from its board snapshot.
 *
 * The board is the source rather than the feed because the feed's card kinds are
 * *derived from these very buckets* (13 §6 / 08 §3): blockers from Blocked
 * tickets, proposals from Shaping ones. Reading the board directly gets the same
 * answer without pulling a page of notification history the rail would throw
 * away — and without touching the feed's seen/divider lifecycle for a project
 * the user is not looking at.
 *
 * Precedence is the contrast budget in code form: anything needing a person wins
 * over anything merely in motion, which wins over rest. A project that is both
 * blocked on a question AND building elsewhere reads as `needs-you`, because the
 * question is the thing the rail exists to surface.
 *
 * `null` (never fetched, or the fetch failed) is `unknown`, not `quiet`.
 */
export function deriveProjectState(board: Board | null): ProjectState {
  if (board === null) {
    return 'unknown';
  }
  if (board.blocked.length > 0 || board.shaping.length > 0) {
    return 'needs-you';
  }
  if (board.working.length > 0) {
    return 'working';
  }
  return 'quiet';
}
