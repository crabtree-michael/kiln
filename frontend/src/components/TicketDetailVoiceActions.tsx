// The ticket detail sheet's footer input cluster (08 §5, 09 §3–§4): the mic orb
// and the keyboard toggle beside it, plus — for as long as an utterance or a
// typed draft is live — the Send and discard (×) actions at the row's other end.
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
//  • **While an input is live, the sheet's trailing slot belongs to the message,
//    not to the ticket.** At rest that slot holds the ticket's management
//    actions — Poke, Delete, Accept. The moment a session starts they withdraw
//    as a group (`TicketDetail`'s `voiceActive`) and Send and × take the slot in
//    place. The user is mid-sentence: what they need under their thumb is "send
//    this" and "drop this", and an accent-filled Accept sitting where Send is
//    about to be is a mis-tap waiting to happen.
//
// So the swap is a swap and not a reflow — one group of controls hands the right
// of the row to another, and nothing on the row changes position. Reading the
// live footer left to right: the mic, a gap, then × and Send.
//
// **Voice stays primary; the keyboard is the second door into the same room.**
// The toggle is offered only at rest, next to the mic, in the lead group the two
// share — so the mic is still the first thing under the thumb and still the first
// child of the cluster (which is what holds it at the row's left edge). The
// moment either input is live the toggle stands down, because from there the mic
// orb itself is the way back to voice (tapping it ends typed input — see
// `useTicketKeyboard`) and a third control in the lead group would only compete
// with it. Both inputs then wear the SAME Send and ×, and both post through the
// same seam, so the row reads identically whichever one the message was composed
// in.
//
// The × here is NOT the dock's ×. The dock's discards the un-committed transcript
// and deliberately leaves the mic alone (the user is still talking, they just want
// that sentence gone). This one is the way *out*: it discards and stops the mic in
// the same tap — or, in typed input, drops the draft and closes the field — which
// is what returns the footer to Accept.
//
// Presentational consumer of `useVoice()`, like `MicButton` and
// `TicketDetailTranscript`: the only state it touches is the typed draft it is
// handed (`keyboard`, owned by `TicketDetailHost` because the field lives in the
// sheet's other dock slot). It requires a `VoiceProvider` ancestor, so only the
// primary screen (mobile shell and desktop alike) wires it in — a read-only sheet
// opens without one and passes nothing.
// `onActiveChange` is how the sheet learns which of the two arrangements to draw
// without subscribing to the voice store itself (see `TicketDetail`'s
// `voiceActive`): it is a *boolean*, so it fires twice an utterance rather than
// once a word, and the screen above re-renders no more often than the layout
// actually changes.
import { useEffect, useRef, type JSX } from 'react';
import { useVoice } from '@/voice/voice-context';
import { MicButton } from '@/components/MicButton';
import type { TicketKeyboard } from '@/components/use-ticket-keyboard';

export interface TicketDetailVoiceActionsProps {
  /** The title of the ticket this cluster sits inside, registered with the voice
   *  store (via `MicButton`) so a message sent from the sheet is prefixed with it
   *  and the brain knows which ticket the comment is about (08 §5). */
  ticketTitle: string;
  /** The sheet's typed-input state, owned by `TicketDetailHost` and shared with
   *  the dock's panel — which is where the field itself renders. Required rather
   *  than optional so a shell cannot wire the cluster with the keyboard half
   *  missing and ship a Send that posts nothing. */
  keyboard: TicketKeyboard;
  /** Told whenever an input session starts or ends — the signal `TicketDetail`
   *  turns into `voiceActive`. Called with the current reading on mount and with
   *  `false` on unmount, so closing the sheet mid-utterance can't leave the next
   *  ticket's footer stuck in the speaking arrangement. */
  onActiveChange?: ((active: boolean) => void) | undefined;
}

export function TicketDetailVoiceActions({
  ticketTitle,
  keyboard,
  onActiveChange,
}: TicketDetailVoiceActionsProps): JSX.Element {
  const { micState, settledText, tailText, pause, cancel, sendNow } = useVoice();

  // There is something on screen to send: settled ink or a still-forming tail,
  // the same gate the dock's own send/× ride (09 §4).
  const hasTranscript = settledText !== '' || tailText !== '';
  const typing = keyboard.open;
  // ...and an input session is *live* on this ticket whenever the mic is on, or
  // there are words waiting to go, or the user is typing. The middle case matters
  // as much as the first: an end-of-turn final pauses the mic while the utterance
  // sits armed in the grace window, and the footer must not snap back to Accept
  // underneath a transcript the user is still deciding about. The third is what
  // hands the trailing slot to a typed message on the same terms as a spoken one.
  const active = micState === 'listening' || hasTranscript || typing;
  // Whether Send has anything to post — the draft while typing, the transcript
  // while speaking. Either way the button holds its place through the session and
  // is merely dead before there is text, so × never shuffles sideways when the
  // first word lands.
  const canSend = typing ? keyboard.draft.trim() !== '' : hasTranscript;

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

  /** The ×: end the session in one tap, whichever input it is. Speaking — drop
   * the un-committed utterance AND stop the mic; both halves are deliberate,
   * since `cancel` alone would clear the words and leave the mic listening, so
   * the footer would stay in the speaking arrangement with nothing said and no
   * obvious way back to Accept. Typing — drop the draft and close the field, the
   * same "way out" read. */
  const discard = (): void => {
    if (typing) {
      keyboard.end();
      return;
    }
    cancel();
    pause();
  };

  /** Send, through whichever seam composed the message. Both land as a
   * `human.message` prefixed with this ticket's title, so the brain cannot tell
   * — and should not be able to tell — which one the user reached for. */
  const send = (): void => {
    if (typing) {
      keyboard.submit();
      return;
    }
    sendNow();
  };

  return (
    <>
      {/* The lead group: the mic and, at rest, the keyboard toggle. It is the
          cluster's first child and the cluster spans the row, which is what holds
          the mic at the row's left edge in every reading. The group itself is
          rendered unconditionally and the mic is always its first child, so the
          mic is never re-parented and never unmounted mid-utterance (that would
          take its ticket-context registration and its volume-glow loop with it —
          and the registration is what prefixes a TYPED message with this ticket
          too, so it has to outlive the switch to the keyboard). */}
      <div data-role="ticket-detail-voice-mic">
        <MicButton ticketContext={ticketTitle} />
        {!active && (
          // The second door: type instead of speaking. Offered only at rest —
          // once either input is live the mic orb is the way back to voice and
          // the trailing slot carries Send and ×, so a toggle here would be a
          // third way to change your mind about the same sentence. Reuses the
          // dock's `dock-keyboard` selector (and glyph) so PrimaryScreen.css
          // skins it exactly like the dock's own toggle, the same borrowing the
          // ×/Send pair below does.
          <button
            type="button"
            data-role="dock-keyboard"
            aria-label="Type a message"
            onClick={keyboard.begin}
          >
            <svg
              viewBox="0 0 24 24"
              width="22"
              height="22"
              aria-hidden="true"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <rect x="2.5" y="4.5" width="19" height="11" rx="2.5" />
              <path d="M6 8h.01M9.8 8h.01M13.6 8h.01M17.4 8h.01" />
              <path d="M6 11.3h.01M9.8 11.3h.01M13.6 11.3h.01M17.4 11.3h.01" />
              <path d="M8.5 13.6h7" />
              <path d="M8.8 21l3.2-3 3.2 3" />
            </svg>
          </button>
        )}
      </div>
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
            // the first partial lands) but can't post an empty message.
            disabled={!canSend}
            onClick={send}
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
