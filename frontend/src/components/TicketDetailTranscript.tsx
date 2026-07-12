// The live-transcript line for the ticket detail sheet's dock (08 §5, 09 §4).
// While the user talks to the brain about the open proposal, the words land here —
// inside the sheet's own dock, above the action buttons — so they can watch the
// transcript without leaving the sheet. (Previously the transcript only landed in
// the main-screen dock *behind* the sheet, hidden under the scrim, so a voice
// session from the proposal sheet gave no on-screen feedback.)
//
// Presentational consumer of `useVoice()`, mirroring the main Dock's transcript
// spans — settled words in ink, the still-forming tail ghosted, a blinking caret
// while listening — but rendered IN FLOW inside the sheet's dock rather than as an
// upward overlay, so the dock grows to fit it (bounded + internally scrolling, see
// TicketDetail.css). It self-gates: nothing renders unless there is transcript text
// on screen, so it is visible only while the user is actively speaking and vanishes
// the moment the utterance is sent or cleared.
//
// Requires a `VoiceProvider` ancestor, so — like the sheet's mic (`voiceControl`) —
// only the primary screen wires it in; a read-only sheet opens without one and
// passes nothing, so this never mounts there.
import { useEffect, useRef, type JSX } from 'react';
import { useVoice } from '@/voice/voice-context';

export function TicketDetailTranscript(): JSX.Element | null {
  const { micState, settledText, tailText } = useVoice();
  const ref = useRef<HTMLDivElement | null>(null);

  // Keep the newest words in view as text streams in. The line is bounded and
  // scrolls internally (see TicketDetail.css); text flows top-to-bottom, so on
  // every settled/tail update we re-pin scrollTop to the bottom — a no-op while
  // the transcript is short enough not to overflow (mirrors the main Dock).
  useEffect(() => {
    const el = ref.current;
    if (el === null) {
      return;
    }
    el.scrollTop = el.scrollHeight;
  }, [settledText, tailText]);

  // Visible only when there is a transcript on screen — i.e. only while the user
  // is actively speaking (text clears the moment the utterance is sent or the ×
  // discards it). Same gate the main Dock uses for its voice field.
  const hasTranscript = settledText !== '' || tailText !== '';
  if (!hasTranscript) {
    return null;
  }

  return (
    <div data-role="ticket-detail-transcript" data-dock-state={micState} ref={ref}>
      {settledText !== '' && <span data-role="ticket-detail-settled">{settledText}</span>}
      {tailText !== '' && (
        <span data-role="ticket-detail-tail" data-ghost="true">
          {tailText}
        </span>
      )}
      {micState === 'listening' && (
        <span data-role="ticket-detail-caret" aria-hidden="true">
          |
        </span>
      )}
    </div>
  );
}
