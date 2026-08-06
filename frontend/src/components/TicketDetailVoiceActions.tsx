// The ticket detail sheet's footer voice cluster (08 §5, 09 §3–§4): the mic orb,
// plus — for as long as a voice session is live — the Send and discard (×)
// actions beside it.
//
// Two things about it are the whole point:
//
//  • **The mic does not move. Ever.** It is the footer's bottom-left control at
//    rest and it is the footer's bottom-left control mid-utterance — same slot,
//    same pixels. It is also the one control the user has just touched, and a
//    button that slides out from under the finger that activated it is exactly
//    the jank this arrangement exists to avoid. (The orb has to stay on screen
//    while speaking regardless: its glow is driven from real input level, so it
//    is the only thing reporting that the mic is live and how loud the room is.)
//  • **While the mic is up, the sheet's trailing slot belongs to the utterance,
//    not to the ticket.** At rest that slot holds the ticket's management
//    actions — Poke, Delete, Accept. The moment a voice session starts they
//    withdraw as a group (`TicketDetail`'s `voiceActive`) and Send and × take
//    the slot in place. The user is mid-sentence: what they need under their
//    thumb is "send this" and "drop this", and an accent-filled Accept sitting
//    where Send is about to be is a mis-tap waiting to happen.
//
// So the swap is a swap and not a reflow — one group of controls hands the right
// of the row to another, and nothing on the row changes position. Reading the
// speaking footer left to right: the mic, a gap, then × and Send.
//
// The × here is NOT the dock's ×. The dock's discards the un-committed transcript
// and deliberately leaves the mic alone (the user is still talking, they just want
// that sentence gone). This one is the way *out*: it discards and stops the mic in
// the same tap, which is what returns the footer to Accept. Inside a sheet that is
// the only exit that reads as one — the sheet has no keyboard toggle and no second
// row of controls to fall back to.
//
// Presentational consumer of `useVoice()`, like `MicButton` and
// `TicketDetailTranscript`: it holds no state of its own and requires a
// `VoiceProvider` ancestor, so only the primary screen (mobile shell and desktop
// alike) wires it in — a read-only sheet opens without one and passes nothing.
// `onActiveChange` is how the sheet learns which of the two arrangements to draw
// without subscribing to the voice store itself (see `TicketDetail`'s
// `voiceActive`): it is a *boolean*, so it fires twice an utterance rather than
// once a word, and the screen above re-renders no more often than the layout
// actually changes.
import { useEffect, useRef, type JSX } from 'react';
import { useVoice } from '@/voice/voice-context';
import { MicButton } from '@/components/MicButton';

export interface TicketDetailVoiceActionsProps {
  /** The title of the ticket this cluster sits inside, registered with the voice
   *  store (via `MicButton`) so a message sent from the sheet is prefixed with it
   *  and the brain knows which ticket the comment is about (08 §5). */
  ticketTitle: string;
  /** Told whenever a voice session starts or ends — the signal `TicketDetail`
   *  turns into `voiceActive`. Called with the current reading on mount and with
   *  `false` on unmount, so closing the sheet mid-utterance can't leave the next
   *  ticket's footer stuck in the speaking arrangement. */
  onActiveChange?: ((active: boolean) => void) | undefined;
}

export function TicketDetailVoiceActions({
  ticketTitle,
  onActiveChange,
}: TicketDetailVoiceActionsProps): JSX.Element {
  const { micState, settledText, tailText, pause, cancel, sendNow } = useVoice();

  // There is something on screen to send: settled ink or a still-forming tail,
  // the same gate the dock's own send/× ride (09 §4).
  const hasTranscript = settledText !== '' || tailText !== '';
  // ...and a voice session is *live* on this ticket whenever the mic is on OR
  // there are words waiting to go. The second half matters as much as the first:
  // an end-of-turn final pauses the mic while the utterance sits armed in the
  // grace window, and the footer must not snap back to Accept underneath a
  // transcript the user is still deciding about.
  const active = micState === 'listening' || hasTranscript;

  // Report the reading upward. Through a ref so a caller passing a fresh arrow
  // each render can't make this fire on every render — only the boolean flipping
  // does. The ref is synced in its own effect declared FIRST, so it is current by
  // the time the reporting effect below runs.
  const reportRef = useRef(onActiveChange);
  useEffect(() => {
    reportRef.current = onActiveChange;
  }, [onActiveChange]);
  useEffect(() => {
    const report = reportRef.current;
    if (report !== undefined) {
      report(active);
    }
  }, [active]);
  // Separately, a mount-only cleanup: the sheet closing (or the ticket changing)
  // unmounts this while a session may still be live, and the flag it set lives on
  // the screen above, which does not unmount with it.
  useEffect(() => {
    return () => {
      const report = reportRef.current;
      if (report !== undefined) {
        report(false);
      }
    };
  }, []);

  /** The ×: drop the un-committed utterance and end the session in one tap. Both
   * halves are deliberate — `cancel` alone would clear the words and leave the
   * mic listening, so the footer would stay in the speaking arrangement with
   * nothing said and no obvious way back to Accept. */
  const discard = (): void => {
    cancel();
    pause();
  };

  return (
    <>
      {/* First child of the cluster, and the cluster spans the row — which is what
          holds the mic at the left edge in both readings. It is rendered
          unconditionally and never re-parented, so it is never unmounted
          mid-utterance (that would take its ticket-context registration and its
          volume-glow loop with it). */}
      <MicButton ticketContext={ticketTitle} />
      {active && (
        // Send and × as one box: the cluster distributes its children with
        // `space-between`, so a loose × would be stranded mid-row instead of
        // arriving beside Send. As the cluster's last child the group lands on the
        // row's right edge — the slot the state actions have just vacated.
        <div data-role="ticket-detail-voice-send">
          {/* Reuse the dock's `dock-cancel`/`dock-send` selectors so
              PrimaryScreen.css skins this pair exactly like the dock's own — the
              sheet borrows one visual language for the mic and its two actions
              rather than growing a second (see the desktop shell's note on the
              mobile sheet's unscoped rules). */}
          <button type="button" data-role="dock-cancel" aria-label="Discard" onClick={discard}>
            <span aria-hidden="true">×</span>
          </button>
          <button
            type="button"
            data-role="dock-send"
            aria-label="Send"
            // Live the moment there are words, dead before then: the button holds
            // its place through the whole session (so × never shuffles sideways as
            // the first partial lands) but can't post an empty utterance.
            disabled={!hasTranscript}
            onClick={sendNow}
          >
            <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
              <path d="M12 4l-8 8h5v8h6v-8h5z" fill="currentColor" />
            </svg>
          </button>
        </div>
      )}
    </>
  );
}
