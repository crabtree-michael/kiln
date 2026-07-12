// The activity row above the dock (08 §4). Pure render of the *current*
// activity state — the store owns the notification stack and each entry's timer,
// so this component only maps the toasts it is handed onto the selector surfaces
// the E2E asserts: `thinking-indicator`, `toast-pill` (+ `data-verb`), `say-pill`.
// Multiple live toasts stack into a list; the spinner shows only when the stack
// is empty.
//
// Neither pill carries an always-on × any more (08 §4). A board `toast` points at
// a ticket: tapping anywhere on it opens that ticket's detail view and dismisses
// the toast in one move. A `say` pill (and an orphan toast with no ticket id) has
// nowhere to route, so tapping OPENS it in place instead — dropping the 2-line
// clamp to reveal the full content and swapping in a Close control, which
// dismisses it entirely (there is no collapse-back); opening pauses that pill's
// auto-dismiss timer so it can't vanish mid-read.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { JSX } from 'react';
import type { ActivityToast, ToastVerb } from '@/stores/activity-context';
import { verbEmoji, verbLabel } from '@/components/feed-format';
import { pickKilnWord } from '@/components/kiln-words';

export interface ActivityRowProps {
  thinking: boolean;
  toasts: ActivityToast[];
  /** Dismisses one pill by id — fired when an open pill's Close control is tapped,
   * or when a board toast is tapped to open its ticket (08 §4). The always-on × is
   * gone; these are the manual dismisses. */
  onDismiss: (id: number) => void;
  /** Opens a board toast's linked ticket in the detail overlay — fired when the
   * toast is tapped (08 §4). Given a non-empty ticket id, tapping opens the ticket
   * and dismisses the toast (via `onDismiss`); without it — a say pill, an orphan
   * toast with no id, or a presentational render that omits this handler — the pill
   * opens in place instead. Optional so presentational tests can render the row
   * without routing. */
  onOpenTicket?: ((ticketId: string) => void) | undefined;
  /** Pauses (`true`) a pill's auto-dismiss timer when it is opened, so it can't
   * vanish while the user reads the full content. Optional so presentational tests
   * can render the row without the store. Closing dismisses the pill outright, so
   * the timer is never resumed. */
  onToastExpandedChange?: ((id: number, expanded: boolean) => void) | undefined;
}

/** A `say` pill: opens in place to reveal the full utterance, and its Close
 * control dismisses it entirely (there is no collapse-back). */
function SayPill({
  id,
  text,
  onDismiss,
  onExpandedChange,
}: {
  id: number;
  text: string;
  onDismiss: (id: number) => void;
  onExpandedChange?: ((id: number, expanded: boolean) => void) | undefined;
}): JSX.Element {
  const [open, setOpen] = useState(false);

  // Opening reveals the full text and pauses the auto-dismiss timer so it can't
  // disappear mid-read; Close is the only way back out and dismisses the pill
  // (there is no collapse-in-place, so the timer is never resumed).
  const openPill = (): void => {
    setOpen(true);
    onExpandedChange?.(id, true);
  };

  return (
    <div data-role="say-pill">
      {open ? (
        <>
          <div data-role="toast-open">
            <span data-role="say-text" data-expanded="true">
              {text}
            </span>
          </div>
          <button
            type="button"
            data-role="toast-close"
            aria-label="Close"
            onClick={() => {
              onDismiss(id);
            }}
          >
            Close
          </button>
        </>
      ) : (
        <button
          type="button"
          data-role="toast-open"
          aria-expanded={false}
          aria-label="Open message"
          onClick={openPill}
        >
          <span data-role="say-text">{text}</span>
        </button>
      )}
    </div>
  );
}

/** A board `toast`: tapping it opens its linked ticket's detail view and dismisses
 * the toast (08 §4). Only an orphan toast — no ticket id, or no `onOpenTicket`
 * handler wired — falls back to opening in place to read its full title, with a
 * Close control that dismisses it (there is no collapse-back). */
function ToastPill({
  id,
  verb,
  ticketTitle,
  ticketId,
  onDismiss,
  onOpenTicket,
  onExpandedChange,
}: {
  id: number;
  verb: ToastVerb;
  ticketTitle: string;
  ticketId: string;
  onDismiss: (id: number) => void;
  onOpenTicket?: ((ticketId: string) => void) | undefined;
  onExpandedChange?: ((id: number, expanded: boolean) => void) | undefined;
}): JSX.Element {
  const [open, setOpen] = useState(false);

  // A board toast points at a ticket, so tapping anywhere on it jumps to that
  // ticket's detail view and dismisses the toast in one move. Only when there's
  // no ticket to route to — an orphan toast with no id, or a presentational render
  // with no handler — does it fall back to opening in place.
  const canOpenTicket = onOpenTicket !== undefined && ticketId !== '';

  // Opening in place reveals the full title and pauses the auto-dismiss timer so
  // it can't disappear mid-read; Close is the only way back out and dismisses the
  // pill (there is no collapse-in-place, so the timer is never resumed).
  const openPill = (): void => {
    setOpen(true);
    onExpandedChange?.(id, true);
  };
  // Tap-to-open: route to the ticket, then dismiss the toast so it doesn't linger
  // over the detail view it just opened.
  const openTicket = (): void => {
    onOpenTicket?.(ticketId);
    onDismiss(id);
  };
  const openLabel = ticketTitle !== '' ? `Open update: ${ticketTitle}` : 'Open update';
  const content = (
    <>
      <span data-role="toast-icon" role="img" aria-label={verbLabel(verb)}>
        {verbEmoji(verb)}
      </span>
      <span data-role="toast-text" data-expanded={open ? 'true' : undefined}>
        <span data-role="toast-title">{ticketTitle}</span>
      </span>
    </>
  );

  if (canOpenTicket) {
    return (
      <div data-role="toast-pill" data-verb={verb}>
        <button type="button" data-role="toast-open" aria-label={openLabel} onClick={openTicket}>
          {content}
        </button>
      </div>
    );
  }

  return (
    <div data-role="toast-pill" data-verb={verb}>
      {open ? (
        <>
          <div data-role="toast-open">{content}</div>
          <button
            type="button"
            data-role="toast-close"
            aria-label="Close"
            onClick={() => {
              onDismiss(id);
            }}
          >
            Close
          </button>
        </>
      ) : (
        <button
          type="button"
          data-role="toast-open"
          aria-expanded={false}
          aria-label={openLabel}
          onClick={openPill}
        >
          {content}
        </button>
      )}
    </div>
  );
}

function ActivityToastPill({
  toast,
  onDismiss,
  onOpenTicket,
  onExpandedChange,
}: {
  toast: ActivityToast;
  onDismiss: (id: number) => void;
  onOpenTicket?: ((ticketId: string) => void) | undefined;
  onExpandedChange?: ((id: number, expanded: boolean) => void) | undefined;
}): JSX.Element {
  const { id, pill } = toast;
  if (pill.kind === 'say') {
    return (
      <SayPill id={id} text={pill.text} onDismiss={onDismiss} onExpandedChange={onExpandedChange} />
    );
  }
  return (
    <ToastPill
      id={id}
      verb={pill.verb}
      ticketTitle={pill.ticketTitle}
      ticketId={pill.ticketId}
      onDismiss={onDismiss}
      onOpenTicket={onOpenTicket}
      onExpandedChange={onExpandedChange}
    />
  );
}

export function ActivityRow({
  thinking,
  toasts,
  onDismiss,
  onOpenTicket,
  onToastExpandedChange,
}: ActivityRowProps): JSX.Element {
  const empty = toasts.length === 0;
  const rowRef = useRef<HTMLDivElement>(null);

  // Swap the static "thinking" for a random clay-work verb (sculpting, molding,
  // firing…) so the pill stays on-brand. Re-rolled each time the pill appears
  // (`thinking` flips true) and held steady while it stays up, so the word
  // doesn't flicker on unrelated re-renders. `thinking` is the intended re-roll
  // trigger even though the callback doesn't read it.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const word = useMemo(() => pickKilnWord(), [thinking]);

  // Keep the feed's last card clear of this band. The activity row is an
  // out-of-flow overlay anchored above the dock (PrimaryScreen.css): when it
  // holds a "Kiln is thinking…" spinner or a toast stack it floats UP over the
  // feed's bottom with an opaque fill, occluding the newest card(s) — and with
  // nothing reserving that space the feed can't be scrolled far enough to reveal
  // them. Mirror the live transcript's `--dock-overlay-height` trick: publish the
  // band's current height as `--feed-bottom-inset` on the screen root so the feed
  // adds exactly that much bottom scroll inset (0px when the band is empty, so the
  // idle layout is untouched), tracked live via ResizeObserver as toasts stack /
  // dismiss and the spinner comes and goes. Written on the screen root (not this
  // row) so it reaches the feed, a distant sibling; a no-op when the row renders
  // outside a primary screen (isolated tests) since `closest` is null.
  useEffect(() => {
    const el = rowRef.current;
    const root = el?.closest<HTMLElement>('[data-role="primary-screen"]') ?? null;
    if (root === null) {
      return;
    }
    const publish = (): void => {
      root.style.setProperty('--feed-bottom-inset', `${(el?.offsetHeight ?? 0).toString()}px`);
    };
    publish();
    if (el === null || typeof ResizeObserver === 'undefined') {
      return () => {
        root.style.removeProperty('--feed-bottom-inset');
      };
    }
    const observer = new ResizeObserver(publish);
    observer.observe(el);
    return () => {
      observer.disconnect();
      root.style.removeProperty('--feed-bottom-inset');
    };
  }, []);

  return (
    <div data-role="activity-row" ref={rowRef}>
      {/* The "Kiln is thinking…" pill renders FIRST — at the top of the flex
          column, farthest from the dock — so it floats clear above any toasts.
          It carries its own opaque background and ember glow (PrimaryScreen.css),
          so unlike the toast stack it does not sit on the dock's page-tone band;
          it reads as an elevated chip hovering over the page. The toast stack
          follows below, nearest the dock, where its band merges with the dock as
          one continuous surface. */}
      {thinking && (
        <div data-role="thinking-indicator">
          <span data-role="thinking-text">{word}…</span>
        </div>
      )}

      {!empty && (
        <div data-role="toast-stack">
          {toasts.map((toast) => (
            <ActivityToastPill
              key={toast.id}
              toast={toast}
              onDismiss={onDismiss}
              onOpenTicket={onOpenTicket}
              onExpandedChange={onToastExpandedChange}
            />
          ))}
        </div>
      )}
    </div>
  );
}
