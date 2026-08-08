// The shell conformance suite (shell-architecture plan, T5).
//
// Every assertion here is about a rule the two feed shells SHARE — where the
// last-seen divider falls, when "Show earlier" is offered, which cards recede as
// already-seen — and each is written once and run against both DOMs through an
// adapter. That is the guard the plan was written to add: before this, a fix to a
// shared rule meant editing two suites, and nothing failed if you only edited
// one. Now one shared fix is asserted in both shells by one test, and a new shell
// joins by adding a row to `SHELLS`.
//
// **This does not replace the per-shell suites**, and shouldn't. `PrimaryScreenView.test.tsx`
// and `DesktopScreenView.test.tsx` still own everything that is genuinely one
// shell's: the phone's swipe, pull-to-refresh and bulk clear; the desk's loading
// line, roving focus and withheld resting state. What lives here is only what
// both must agree on.
//
// Two constraints on how it mounts, both from the plan's §4:
//
//   * jsdom has no `matchMedia`, so `useIsDesktop()` is always false. The suite
//     therefore renders each VIEW directly, exactly as the existing shell tests
//     do — never `PrimaryScreen`, which would only ever give us the phone.
//   * Each shell keeps its own selectors. The rules are shared; the DOM is not,
//     and an adapter that had to reach for the same element in both would be
//     asserting the shells are the same file.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, type RenderResult } from '@testing-library/react';
import { PrimaryScreenView } from '@/components/PrimaryScreenView';
import { DesktopScreenView } from '@/components/desktop/DesktopScreenView';
import type { VoiceStoreValue } from '@/voice/voice-context';
import { makeFeedCard, makeFeedSnapshot } from '@/test/fixtures';
import type { FeedCard, FeedSnapshot } from '@/transport/transport';

// Both shells mount the ticket sheet's voice cluster, a live `useVoice()`
// consumer. These are presentational renders with no `VoiceProvider`, so the
// store is mocked at its resting state — `paused`, which is what the app always
// opens at. A `listening` mock would put every sheet into the speaking
// arrangement and make Accept vanish from tests that never mentioned voice.
let mockVoiceValue: VoiceStoreValue;

function restingVoice(): VoiceStoreValue {
  return {
    micState: 'paused',
    connecting: false,
    settledText: '',
    tailText: '',
    pause: vi.fn(),
    resume: vi.fn(),
    cancel: vi.fn(),
    sendNow: vi.fn(),
    countingDown: false,
    sendImminent: false,
    delaySend: vi.fn(),
    getSendCountdown: vi.fn(() => null),
    editing: false,
    beginEdit: vi.fn(),
    editTranscript: vi.fn(),
    endEdit: vi.fn(),
    getLevel: vi.fn(() => 0),
    keyboardMode: false,
    openKeyboard: vi.fn(),
    closeKeyboard: vi.fn(),
    submitText: vi.fn(() => Promise.resolve(true)),
    setTicketContext: vi.fn(),
  };
}

vi.mock('@/voice/voice-context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/voice/voice-context')>();
  return { ...actual, useVoice: (): VoiceStoreValue => mockVoiceValue };
});

beforeEach(() => {
  mockVoiceValue = restingVoice();
});

const NOW = Date.parse('2026-08-08T12:00:00Z');

/** The shared props any feed shell can be handed. Deliberately the INTERSECTION
 * of the two prop types — a shell-specific prop has no business in a test about
 * what the shells agree on. */
interface SharedProps {
  /** Required, like the shells' own prop — every case here is about what a
   * particular feed renders as, so there is no "didn't say" reading. */
  feed: FeedSnapshot | null;
  lastSeenId?: number | null;
  hasEarlier?: boolean;
  loadingEarlier?: boolean;
  onShowEarlier?: (() => void) | undefined;
}

/** How to mount one shell and where to look in the DOM it produces. */
interface ShellAdapter {
  name: string;
  render: (props: SharedProps) => RenderResult;
  /** The per-card wrapper, one per card in feed order — the element that holds
   * both the card and, on the row it falls above, the divider. The two shells
   * genuinely differ here and are meant to: the phone's `backlog-slot` contains
   * the divider, while the desk puts it in the `<li>` ABOVE its focusable row
   * (§4.3 of the plan — the divider's index is shared, its wrapper is not).
   * Asking each shell for its own wrapper is what lets one assertion about
   * WHERE the divider falls run against both. */
  rowSelector: string;
  /** The element carrying the seen de-emphasis, looked up within a row. */
  seenSelector: string;
  /** The scroll wrapper "Show earlier" must be the last child of — the
   * containing block its `position: sticky` hangs off. Each shell's own. */
  scrollSelector: string;
}

const SHELLS: ShellAdapter[] = [
  {
    name: 'phone',
    render: (props) => render(<PrimaryScreenView {...BASE_PHONE} {...props} />),
    rowSelector: '[data-role="backlog-slot"]',
    seenSelector: '[data-role="feed-card-body"][data-seen="true"]',
    scrollSelector: '[data-role="backlog"]',
  },
  {
    name: 'desk',
    render: (props) => render(<DesktopScreenView {...BASE_DESK} {...props} />),
    rowSelector: '[data-role="desktop-feed-list"] > li',
    seenSelector: '[data-role="feed-card-body"][data-seen="true"]',
    scrollSelector: '[data-role="desktop-feed-scroll"]',
  },
];

const BASE_PHONE = {
  connectionState: 'connected' as const,
  thinking: false,
  toasts: [],
  onDismiss: vi.fn(),
  onAccept: vi.fn(),
  now: NOW,
};

const BASE_DESK = {
  projects: [],
  currentProjectId: null,
  onSelectProject: vi.fn(),
  connectionState: 'connected' as const,
  thinking: false,
  toasts: [],
  onDismiss: vi.fn(),
  onAccept: vi.fn(),
  now: NOW,
};

function update(id: string, notificationId: number): FeedCard {
  return makeFeedCard({
    kind: 'update',
    id,
    label: 'Kiln',
    body: `body ${id}`,
    createdAt: '2026-08-08T11:00:00Z',
    notificationId,
  });
}

function done(id: string, notificationId: number): FeedCard {
  return makeFeedCard({
    kind: 'done',
    id,
    label: 'Done',
    body: `finished ${id}`,
    createdAt: '2026-08-08T11:00:00Z',
    notificationId,
  });
}

describe.each(SHELLS)('$name shell — the rules both shells share', (shell) => {
  /** The index of the row the divider is drawn above, or -1. Each shell wraps the
   * divider in its own element, so this asks WHICH ROW rather than which parent. */
  function dividerRow(container: HTMLElement): number {
    const rows = Array.from(container.querySelectorAll<HTMLElement>(shell.rowSelector));
    return rows.findIndex((row) => row.querySelector('[data-role="feed-divider"]') !== null);
  }

  describe('the last-seen divider', () => {
    it('falls above the first card at or below the boundary', () => {
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9), update('c2', 5), update('c3', 2)] }),
        lastSeenId: 6,
      });
      expect(dividerRow(container)).toBe(1);
    });

    it('is not drawn when everything is new', () => {
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9), update('c2', 8)] }),
        lastSeenId: 3,
      });
      expect(dividerRow(container)).toBe(-1);
    });

    it('is not drawn when nothing is new — a line labelling the whole feed says nothing', () => {
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 2), update('c2', 1)] }),
        lastSeenId: 3,
      });
      expect(dividerRow(container)).toBe(-1);
    });

    it('is not drawn at all without a boundary', () => {
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9), update('c2', 1)] }),
        lastSeenId: null,
      });
      expect(dividerRow(container)).toBe(-1);
    });

    it('skips the mechanical done notice — the boundary is about what Kiln SAID', () => {
      // The regression the shared model's two-name split exists to prevent, now
      // asserted through the rendered DOM of both shells rather than only in the
      // model's own unit test.
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9), done('c2', 2), update('c3', 1)] }),
        lastSeenId: 3,
      });
      expect(dividerRow(container)).toBe(2);
    });

    it('is drawn at most once', () => {
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9), update('c2', 2), update('c3', 1)] }),
        lastSeenId: 3,
      });
      expect(container.querySelectorAll('[data-role="feed-divider"]')).toHaveLength(1);
    });
  });

  describe('seen de-emphasis', () => {
    it('recedes the cards at or below the boundary and no others', () => {
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9), update('c2', 2)] }),
        lastSeenId: 3,
      });
      const rows = Array.from(container.querySelectorAll<HTMLElement>(shell.rowSelector));
      expect(rows[0]?.querySelector(shell.seenSelector)).toBeNull();
      expect(rows[1]?.querySelector(shell.seenSelector)).not.toBeNull();
    });

    it('never recedes a mechanical notice, whatever its id', () => {
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9), done('c2', 1)] }),
        lastSeenId: 3,
      });
      const rows = Array.from(container.querySelectorAll<HTMLElement>(shell.rowSelector));
      expect(rows[1]?.querySelector(shell.seenSelector)).toBeNull();
    });
  });

  describe('"Show earlier" — one control, one label, in every state', () => {
    const CARDS: { state: string; cards: FeedCard[] }[] = [
      { state: 'a full feed', cards: [update('c1', 9), update('c2', 1)] },
      { state: 'a single card', cards: [update('c1', 9)] },
      // The state that matters most: everything has collapsed away, so the shell
      // is at rest — and that is precisely when the way back has to be there.
      { state: 'an empty feed', cards: [] },
    ];

    it.each(CARDS)('is offered over $state', ({ cards }) => {
      shell.render({
        feed: makeFeedSnapshot({ cards }),
        hasEarlier: true,
        onShowEarlier: vi.fn(),
      });
      expect(screen.getByRole('button', { name: 'Show earlier' })).toBeInTheDocument();
    });

    it('is absent when there is nothing further back', () => {
      shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9)] }),
        hasEarlier: false,
        onShowEarlier: vi.fn(),
      });
      expect(screen.queryByRole('button', { name: 'Show earlier' })).toBeNull();
    });

    it('is absent when the shell wired no handler for it', () => {
      // The same optional-prop gating every affordance uses: a presentational
      // render that omits the callback shows no control at all.
      shell.render({ feed: makeFeedSnapshot({ cards: [update('c1', 9)] }), hasEarlier: true });
      expect(screen.queryByRole('button', { name: 'Show earlier' })).toBeNull();
    });

    it('disables while a page is in flight, and never renames itself', () => {
      shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9)] }),
        hasEarlier: true,
        loadingEarlier: true,
        onShowEarlier: vi.fn(),
      });
      const button = screen.getByRole('button', { name: 'Show earlier' });
      expect(button).toBeDisabled();
      expect(button).toHaveAttribute('aria-busy', 'true');
    });

    it('is the LAST child of this shell’s own scroll wrapper', () => {
      // The single highest regression risk the plan names (§4.4). The control's
      // `position: sticky` and `margin-top: auto` both hang off this wrapper, so
      // a shared component hoisted to a common parent would stop being the foot
      // of the feed region — the bug that was fixed five times. jsdom cannot see
      // the geometry; `tests/layout/` measures it in a browser. This asserts the
      // structural half, in both shells, from one test.
      const { container } = shell.render({
        feed: makeFeedSnapshot({ cards: [update('c1', 9)] }),
        hasEarlier: true,
        onShowEarlier: vi.fn(),
      });
      const scroll = container.querySelector(shell.scrollSelector);
      expect(scroll?.lastElementChild).toHaveAttribute('data-role', 'feed-show-earlier');
    });
  });

  describe('card membership and order', () => {
    it('renders one row per card, in the order the snapshot gave them', () => {
      const cards = [update('c1', 9), done('c2', 8), update('c3', 7)];
      const { container } = shell.render({ feed: makeFeedSnapshot({ cards }) });
      const rows = container.querySelectorAll<HTMLElement>(shell.rowSelector);
      expect(rows).toHaveLength(3);
      expect(Array.from(rows).map((row) => row.textContent.includes('body c1'))).toEqual([
        true,
        false,
        false,
      ]);
    });

    it('renders no rows for a null feed', () => {
      const { container } = shell.render({ feed: null });
      expect(container.querySelectorAll(shell.rowSelector)).toHaveLength(0);
    });
  });
});
