// The activity row above the dock (08 §4). Pure render of the *current*
// activity state — the store owns the notification stack and each entry's timer,
// so this component only maps the toasts it is handed onto the selector surfaces
// the E2E asserts: `thinking-indicator`, `toast-pill` (+ `data-verb`), `say-pill`.
// Multiple live toasts stack into a list; the spinner shows only when the stack
// is empty.
//
// No pill carries a close control of any kind (08 §4) — not the old always-on ×,
// and not the Close button that briefly replaced it. A pill is one tap target in
// every state, and the tap does the one thing that pill has left to do:
//   - a board `toast` points at a ticket, so tapping anywhere on it opens that
//     ticket's detail view and dismisses the toast in one move;
//   - a pill with nowhere to route (a `say`, or an orphan toast with no ticket
//     id) that is HIDING content — its 2-line clamp actually overflows — expands
//     in place on the first tap, and the next tap dismisses it;
//   - a pill with nowhere to route and nothing more to show dismisses on the
//     first tap, since expanding would reveal nothing.
// Expanding pauses that pill's auto-dismiss timer so it can't vanish mid-read;
// there is no collapse-back, so the timer is never resumed.
//
// Whether a pill "can expand" is a measured fact, not a guess: `useClampOverflow`
// asks the clamped text whether it overflows (the same question the feed card's
// "tap to see more" cue asks). jsdom does no layout, so under test every pill
// reads as non-expandable unless the heights are faked.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { JSX } from 'react';
import type { ActivityToast, ToastVerb } from '@/stores/activity-context';
import { verbEmoji, verbLabel } from '@/components/feed-format';
import { pickKilnWord } from '@/components/kiln-words';
import { useClampOverflow } from '@/components/use-clamp-overflow';

export interface ActivityRowProps {
  thinking: boolean;
  toasts: ActivityToast[];
  /** Dismisses one pill by id — fired when a tap has nothing left to do: an
   * already-expanded pill, a pill with nothing to expand, or a board toast tapped
   * to open its ticket (08 §4). There is no close control; these are the manual
   * dismisses. */
  onDismiss: (id: number) => void;
  /** Opens a board toast's linked ticket in the detail overlay — fired when the
   * toast is tapped (08 §4). Given a non-empty ticket id, tapping opens the ticket
   * and dismisses the toast (via `onDismiss`); without it — a say pill, an orphan
   * toast with no id, or a presentational render that omits this handler — the pill
   * opens in place instead. Optional so presentational tests can render the row
   * without routing. */
  onOpenTicket?: ((ticketId: string) => void) | undefined;
  /** Pauses (`true`) a pill's auto-dismiss timer when it is expanded, so it can't
   * vanish while the user reads the full content. Optional so presentational tests
   * can render the row without the store. The tap after that dismisses the pill
   * outright, so the timer is never resumed. */
  onToastExpandedChange?: ((id: number, expanded: boolean) => void) | undefined;
}

/** A `say` pill: expands in place to reveal the rest of a clamped utterance, and
 * the next tap dismisses it (there is no collapse-back). An utterance that
 * already fits skips the expand step — its first tap dismisses. */
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
  // Only meaningful while collapsed — once open the clamp is gone, so freeze the
  // measurement rather than let an unclamped element read as "fits".
  const { ref, truncated } = useClampOverflow<HTMLSpanElement>(text, !open);
  const expands = !open && truncated;

  // Expanding reveals the full text and pauses the auto-dismiss timer so it can't
  // disappear mid-read; the next tap dismisses the pill (there is no
  // collapse-in-place, so the timer is never resumed).
  const handleClick = (): void => {
    if (!expands) {
      onDismiss(id);
      return;
    }
    setOpen(true);
    onExpandedChange?.(id, true);
  };

  return (
    <div data-role="say-pill">
      <button
        type="button"
        data-role="toast-open"
        // Only claim an expandable state when there is one: a pill with nothing
        // more to show is a plain dismiss target, not a collapsed disclosure.
        aria-expanded={open || truncated ? open : undefined}
        aria-label={expands ? 'Open message' : 'Dismiss message'}
        onClick={handleClick}
      >
        <span ref={ref} data-role="say-text" data-expanded={open ? 'true' : undefined}>
          {text}
        </span>
      </button>
    </div>
  );
}

/** A board `toast`: tapping it opens its linked ticket's detail view and dismisses
 * the toast (08 §4). Only an orphan toast — no ticket id, or no `onOpenTicket`
 * handler wired — falls back to expanding in place to read a clamped title, with
 * the next tap dismissing it (there is no collapse-back); a title that already
 * fits dismisses on the first tap. */
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
  // Only meaningful while collapsed (see SayPill) — and never for a toast that
  // routes to a ticket, which has no expanded state at all.
  const { ref, truncated } = useClampOverflow<HTMLSpanElement>(ticketTitle, !open);

  // A board toast points at a ticket, so tapping anywhere on it jumps to that
  // ticket's detail view and dismisses the toast in one move. Only when there's
  // no ticket to route to — an orphan toast with no id, or a presentational render
  // with no handler — does it fall back to expanding in place.
  const canOpenTicket = onOpenTicket !== undefined && ticketId !== '';
  const expands = !open && truncated;

  // Expanding in place reveals the full title and pauses the auto-dismiss timer so
  // it can't disappear mid-read; the next tap dismisses the pill (there is no
  // collapse-in-place, so the timer is never resumed).
  const handleClick = (): void => {
    if (!expands) {
      onDismiss(id);
      return;
    }
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
  const dismissLabel = ticketTitle !== '' ? `Dismiss update: ${ticketTitle}` : 'Dismiss update';
  const content = (
    <>
      <span data-role="toast-icon" role="img" aria-label={verbLabel(verb)}>
        {verbEmoji(verb)}
      </span>
      <span ref={ref} data-role="toast-text" data-expanded={open ? 'true' : undefined}>
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
      <button
        type="button"
        data-role="toast-open"
        aria-expanded={open || truncated ? open : undefined}
        aria-label={expands ? openLabel : dismissLabel}
        onClick={handleClick}
      >
        {content}
      </button>
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
  // out-of-flow overlay
  // anchored above the dock (PrimaryScreen.css): when it holds a "Kiln is
  // thinking…" spinner or a toast stack it floats UP over the feed's bottom with
  // an opaque fill, occluding whatever ends the feed — and with nothing reserving
  // that space the feed can't be scrolled far enough to reveal it. Mirror the
  // live transcript's `--dock-overlay-height` trick: publish the band's current
  // height as `--feed-bottom-inset` on the screen root so the feed adds exactly
  // that much bottom inset (0px when the band is empty, so the idle layout is
  // untouched), tracked live via ResizeObserver as toasts stack / dismiss and the
  // spinner comes and goes. Written on the screen root (not this row) so it
  // reaches the feed, a distant sibling.
  //
  // BOTH shell roots are named, and the desktop one is not an afterthought: this
  // row is mounted by `DesktopScreenView` too, over a feed whose foot carries the
  // same pinned control, and a selector that knew only about the phone left the
  // desk with no reserve at all — the band simply covered "Show earlier"
  // whenever a toast was up. `closest` is null outside either shell (isolated
  // tests), where the whole effect is a no-op.
  //
  // The reserve is for the CARDS, though, and the control now spends it
  // differently: it cancels this inset back out of its own position (a
  // paint-only `transform`, both stylesheets) so a toast OVERLAYS it rather than
  // pushing it up the screen. That reader assumes exactly what is published here
  // — THIS ROW's whole height, including the gap it rests at when empty.
  // Narrowing it to, say, the toast stack alone would quietly move the control
  // instead of holding it still.
  useEffect(() => {
    const el = rowRef.current;
    const root =
      el?.closest<HTMLElement>('[data-role="primary-screen"], [data-role="desktop-screen"]') ??
      null;
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
