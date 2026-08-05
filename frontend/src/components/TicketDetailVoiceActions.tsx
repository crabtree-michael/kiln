// The ticket detail sheet's footer voice cluster (08 §5, 09 §3–§4): the mic orb,
// plus — for as long as a voice session is live — the Send and discard (×)
// actions beside it.
//
// Two things about it are the whole point, and both are about the *right* of the
// footer rather than the left:
//
//  • **While the mic is up, the sheet's trailing slot belongs to the utterance,
//    not to the ticket.** At rest the cluster is a lone mic at the footer's
//    bottom-left and Accept sits opposite it in the primary slot; the moment a
//    voice session starts, the whole cluster shifts across to join Send and ×
//    on the right and Accept withdraws (`TicketDetail`'s `voiceActive`). The
//    user is mid-sentence: what they need under their thumb is "send this" and
//    "drop this", and an accent-filled Accept sitting where Send is about to be
//    is a mis-tap waiting to happen.
//  • **The mic goes WITH them.** The orb is the expression indicator — it is what
//    reports that the mic is live and how loud the room is (its glow is driven
//    from real input level) — so it has to stay on screen while speaking, beside
//    the two actions that act on what it is hearing, not stranded across the
//    footer from them.
//
// Reading the trailing group right-to-left: Send (the primary, in the slot Accept
// vacated), then ×, then the mic.
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
      <MicButton ticketContext={ticketTitle} />
      {active && (
        <>
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
            // its place through the whole session (so × and the mic never shuffle
            // sideways as the first partial lands) but can't post an empty
            // utterance.
            disabled={!hasTranscript}
            onClick={sendNow}
          >
            <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
              <path d="M12 4l-8 8h5v8h6v-8h5z" fill="currentColor" />
            </svg>
          </button>
        </>
      )}
    </>
  );
}
